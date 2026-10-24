// Package stage implements stages: updates to OCFL objects that are being
// prepared, saved as JSON in stage files, with a record of the local files
// that their new content is read from.
package stage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	ocflstage "github.com/srerickson/ocfl-go/stage"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/statcache"
)

// maximum number of files listed in an error
const maxFileErrors = 5

// Stage is a draft or finalized update to an OCFL object, with a record of
// the local file that each logical path added to it was read from. The record
// includes the file's stat and digest, so staged files that haven't changed
// aren't read again by [Stage.Check], or by [Stage.AddDir] when a directory
// is added again, the way git uses its index for 'git status' and 'git add'.
//
// Logical paths in the object's head version that weren't added to the stage
// have no record. The record for a logical path is dropped when it is
// removed from the update or its digest changes.
type Stage struct {
	// Compare sets the file metadata that AddDir, AddFile and Check compare
	// to find files that haven't changed. It isn't saved in the stage file.
	Compare statcache.CompareMode

	root    string // location of the object's storage root
	update  *ocfl.ObjectUpdate
	sources []string              // absolute directories that files were added from
	files   map[string]stagedFile // logical path -> file it was added from
}

// New returns a new draft stage for the object id in root, which is at the
// location rootLoc. If the object doesn't exist, it is created with the digest
// algorithm alg; otherwise, the stage uses the object's digest algorithm.
func New(ctx context.Context, root *ocfl.Root, rootLoc string, id string, alg digest.Algorithm) (*Stage, error) {
	if rootLoc == "" {
		return nil, errors.New("storage root location is empty")
	}
	u, obj, err := root.NewObjectUpdate(ctx, id)
	if err != nil {
		return nil, err
	}
	if !obj.Exists() && u.DigestAlgorithm().ID() != alg.ID() {
		u, _, err = root.NewObjectUpdate(ctx, id, ocfl.NewUpdateWithDigestAlgorithm(alg))
		if err != nil {
			return nil, err
		}
	}
	return &Stage{root: rootLoc, update: u}, nil
}

// Read reads the stage file name.
func Read(name string) (*Stage, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var s Stage
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("reading stage file %s: %w", name, err)
	}
	return &s, nil
}

// Write writes s to the file name as JSON. The file is replaced atomically, so
// an interrupted Write leaves the previous stage file in place.
func Write(name string, s *Stage) error {
	return write(name, s, func(tmp string) error { return os.Rename(tmp, name) })
}

// WriteNew is like [Write], but it doesn't replace an existing file: if the
// file name exists, it returns an error that wraps [fs.ErrExist].
func WriteNew(name string, s *Stage) error {
	return write(name, s, func(tmp string) error {
		err := os.Link(tmp, name)
		if err == nil || errors.Is(err, fs.ErrExist) {
			return err
		}
		// No hard links: reserve name with an empty file, then replace it.
		// Until it's replaced, the empty file can't be read as a stage.
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		f.Close()
		if err := os.Rename(tmp, name); err != nil {
			os.Remove(name)
			return err
		}
		return nil
	})
}

// write writes s as JSON to a temporary file in name's directory, then calls
// place to move it to name. The temporary file is removed if it's still there
// when place returns. The directory is synced after place succeeds, so the
// file isn't lost if the system crashes.
func write(name string, s *Stage, place func(tmp string) error) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // fails after a successful rename
	if _, err := io.Copy(tmp, bytes.NewReader(b)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0644); err != nil {
		return err
	}
	if err := place(tmp.Name()); err != nil {
		return err
	}
	syncDir(filepath.Dir(name))
	return nil
}

// syncDir syncs the directory dir. Errors are ignored: not all systems can
// sync directories.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// Root returns the location of the storage root with the object being
// updated.
func (s *Stage) Root() string { return s.root }

// ID returns the ID of the object being updated.
func (s *Stage) ID() string { return s.update.ID() }

// NextHead returns the version number of the new version.
func (s *Stage) NextHead() ocfl.VNum { return s.update.NextHead() }

// DigestAlgorithm returns the update's digest algorithm.
func (s *Stage) DigestAlgorithm() digest.Algorithm { return s.update.DigestAlgorithm() }

// Finalized returns true if the update has been finalized for a commit.
func (s *Stage) Finalized() bool { return s.update.Finalized() }

// Update returns the stage's update. Records of staged files for logical
// paths that are removed or changed in the update are dropped.
func (s *Stage) Update() *ocfl.ObjectUpdate { return s.update }

// Finalize finalizes the update for a commit. See [ocfl.ObjectUpdate.Finalize].
func (s *Stage) Finalize(msg string, user ocfl.User) error {
	return s.update.Finalize(msg, user)
}

// AddDir adds the files in the directory dir to s. By default, files are
// added to the top-level directory of the stage and hidden files are
// included. See [AddOption] functions for ways to change this. Symbolic links
// in dir aren't followed: they are an error. Files that were added before
// from the same path aren't read again unless their stat has changed (see
// [statcache]). Changes to the stage state are logged to logger, which may be
// nil. If there is an error, s is unchanged.
func (s *Stage) AddDir(ctx context.Context, dir string, logger *slog.Logger, opts ...AddOption) error {
	conf := newAddConfig(opts...)
	if conf.as == "" {
		conf.as = "."
	}
	if !fs.ValidPath(conf.as) {
		return fmt.Errorf("invalid directory name: %s", conf.as)
	}
	dir, err := resolvePath(dir)
	if err != nil {
		return err
	}
	names, err := listFiles(ctx, dir, conf.noHidden)
	if err != nil {
		return err
	}
	entries, err := s.check(ctx, dir, names, conf.gos)
	if err != nil {
		return err
	}
	u := s.update
	if conf.remove {
		// Remove files before adding to get rid of potential conflicting
		// paths. Files in the stage under 'as' that aren't in dir aren't
		// added back. They're removed from a copy of the update, so s is
		// unchanged if adding fails.
		if u, err = s.cloneUpdate(); err != nil {
			return err
		}
		switch conf.as {
		case ".":
			err = u.Clear()
		default:
			err = u.Remove(conf.as)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return s.add(u, dir, names, entries, func(name string) string { return path.Join(conf.as, name) }, logger)
}

// AddFile adds the file name to s using the file's base name. If name is a
// symbolic link, the file it links to is added with name's base name. Use
// the [AddAs] option to change the logical name for the staged file. Changes
// to the stage state are logged to logger, which may be nil. If there is an
// error, s is unchanged.
func (s *Stage) AddFile(ctx context.Context, name string, logger *slog.Logger, opts ...AddOption) error {
	conf := newAddConfig(opts...)
	if conf.as == "" {
		conf.as = filepath.Base(name)
	}
	if conf.as == "." || !fs.ValidPath(conf.as) {
		return fmt.Errorf("invalid file name: %s", conf.as)
	}
	name, err := resolvePath(name)
	if err != nil {
		return err
	}
	dir, names := filepath.Dir(name), []string{filepath.Base(name)}
	entries, err := s.check(ctx, dir, names, 1)
	if err != nil {
		return err
	}
	return s.add(s.update, dir, names, entries, func(string) string { return conf.as }, logger)
}

// Remove removes logicalPath from the stage state. If recursive is true,
// logicalPath may be a directory ("." for all files), and all files under it
// are removed. Removed files are logged to logger, which may be nil.
func (s *Stage) Remove(logicalPath string, recursive bool, logger *slog.Logger) error {
	name := path.Clean(logicalPath)
	before := s.update.NewState().PathMap()
	if _, isFile := before[name]; !isFile && !recursive {
		if name == "." || hasDir(before, name) {
			return fmt.Errorf("can't remove %q: it is a directory (use recursive removal)", logicalPath)
		}
	}
	var err error
	switch name {
	case ".":
		err = s.update.Clear()
	default:
		err = s.update.Remove(name)
	}
	if err != nil {
		return err
	}
	s.prune()
	logChanges(logger, before, s.update.NewState().PathMap())
	return nil
}

// List writes the logical paths in the stage state to w, one per line,
// optionally preceded by their digests.
func (s *Stage) List(w io.Writer, withDigests bool) {
	for p, digest := range s.update.NewState().PathMap().SortedPaths() {
		if withDigests {
			fmt.Fprintln(w, digest, p)
			continue
		}
		fmt.Fprintln(w, p)
	}
}

// SourceFile returns the path of the file that logicalPath was added from.
// It returns false if logicalPath wasn't added to the stage.
func (s *Stage) SourceFile(logicalPath string) (string, bool) {
	s.prune()
	f, ok := s.files[logicalPath]
	if !ok {
		return "", false
	}
	return s.sourcePath(f), true
}

// FileChange is a staged file that has changed since it was added.
type FileChange struct {
	Path   string               // logical path
	Source string               // file the logical path was added from
	Status statcache.FileStatus // StatusModified or StatusMissing; zero if Err isn't nil
	Err    error                // error checking the file
}

// CheckResult is the result of [Stage.Check].
type CheckResult struct {
	// Changed are the staged files that have changed since they were added,
	// sorted by logical path.
	Changed []FileChange
	// Unavailable are the logical paths, sorted, whose content is new in the
	// update but isn't in any unchanged staged file. The stage can't be
	// committed until they are added again or removed (see [UnavailableError]).
	Unavailable []string
	// Refreshed is true if the recorded stat of any unchanged file was
	// updated: save the stage to keep it, so the next check doesn't read the
	// file.
	Refreshed bool
}

// Check checks the files that were added to s for changes, like 'git status'.
// Files are read only if their stat differs from the one recorded when they
// were added, or if the recorded stat is too recent to be trusted (see
// [statcache]). Records of files that haven't changed but were read are
// refreshed in s. Check returns an error only if ctx is canceled: errors
// checking files are in the result.
func (s *Stage) Check(ctx context.Context) (*CheckResult, error) {
	s.prune()
	paths := slices.Sorted(maps.Keys(s.files))
	items := make([]statcache.Item, len(paths))
	for i, p := range paths {
		f := s.files[p]
		items[i] = statcache.Item{Path: s.sourcePath(f), Cached: &f.Entry}
	}
	results, err := s.checker(0).Check(ctx, items)
	if err != nil {
		return nil, err
	}
	result := &CheckResult{}
	available := map[string]bool{} // digests in unchanged files
	for i, r := range results {
		p := paths[i]
		if r.Err == nil && r.Status == statcache.StatusUnchanged {
			available[r.Entry.Digest] = true
			if f := s.files[p]; r.Entry != f.Entry {
				f.Entry = r.Entry
				s.files[p] = f
				result.Refreshed = true
			}
			continue
		}
		result.Changed = append(result.Changed, FileChange{
			Path:   p,
			Source: items[i].Path,
			Status: r.Status,
			Err:    r.Err,
		})
	}
	needed := map[string]bool{}
	for _, dig := range s.update.NewContentDigests() {
		needed[dig] = !available[dig]
	}
	for p, dig := range s.update.NewState().PathMap().SortedPaths() {
		if needed[dig] {
			result.Unavailable = append(result.Unavailable, p)
		}
	}
	return result, nil
}

// Content returns a content source for the update's new content, read from
// staged files that haven't changed since they were added (see
// [Stage.Check]). Each directory that files were added from is opened as an
// FS with open. Content in more than one unchanged file is read from the file
// for the first logical path, in sorted order. If any content the update
// needs isn't in an unchanged file, Content returns an *[UnavailableError].
func (s *Stage) Content(ctx context.Context, open func(dir string) (ocflfs.FS, error)) (*ocflstage.FileContentMap, error) {
	result, err := s.Check(ctx)
	if err != nil {
		return nil, err
	}
	if len(result.Unavailable) > 0 {
		return nil, &UnavailableError{Paths: result.Unavailable}
	}
	changed := map[string]bool{}
	for _, c := range result.Changed {
		changed[c.Path] = true
	}
	content := &ocflstage.FileContentMap{}
	opened := map[int]ocflfs.FS{}
	for _, p := range slices.Sorted(maps.Keys(s.files)) {
		f := s.files[p]
		if changed[p] || !s.update.NeedsContent(f.Digest) {
			continue
		}
		if fsys, _ := content.GetContent(f.Digest); fsys != nil {
			continue
		}
		fsys, ok := opened[f.Src]
		if !ok {
			if fsys, err = open(s.sources[f.Src]); err != nil {
				return nil, err
			}
			opened[f.Src] = fsys
		}
		content.StageFile(f.Digest, fsys, f.Name)
	}
	return content, nil
}

// UnavailableError is returned by [Stage.Content] when content the update
// needs isn't in any unchanged staged file. It wraps [ocfl.ErrMissingContent].
type UnavailableError struct {
	Paths []string // logical paths with unavailable content, sorted
}

func (e *UnavailableError) Error() string {
	listed := e.Paths[:min(len(e.Paths), maxFileErrors)]
	list := strings.Join(listed, ", ")
	if more := len(e.Paths) - len(listed); more > 0 {
		list += fmt.Sprintf(", and %d more", more)
	}
	return fmt.Sprintf("content for %d file(s) has changed or is missing: %s", len(e.Paths), list)
}

func (e *UnavailableError) Unwrap() error { return ocfl.ErrMissingContent }

// MarshalJSON implements [json.Marshaler] for *Stage.
func (s *Stage) MarshalJSON() ([]byte, error) {
	s.prune()
	j := stageJSON{
		Root:    s.root,
		Update:  s.update,
		Sources: []string{},
		Files:   make(map[string]stagedFile, len(s.files)),
	}
	// only save sources that are used, in the order they were added
	srcIndex := map[int]int{} // index in s.sources -> index in j.Sources
	for _, p := range slices.Sorted(maps.Keys(s.files)) {
		f := s.files[p]
		idx, ok := srcIndex[f.Src]
		if !ok {
			idx = len(j.Sources)
			j.Sources = append(j.Sources, s.sources[f.Src])
			srcIndex[f.Src] = idx
		}
		f.Src = idx
		j.Files[p] = f
	}
	return json.Marshal(j)
}

// UnmarshalJSON implements [json.Unmarshaler] for *Stage. It replaces s with
// the stage saved in data, which must have a storage root location, an update,
// and a record for each staged file that matches the update.
func (s *Stage) UnmarshalJSON(data []byte) error {
	var j stageJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return fmt.Errorf("decoding stage: %w", err)
	}
	if j.Root == "" {
		return errors.New("stage has no storage root location")
	}
	if j.Update == nil {
		return errors.New("stage has no update")
	}
	for i, src := range j.Sources {
		if !filepath.IsAbs(src) {
			return fmt.Errorf("stage source %d isn't an absolute path: %q", i, src)
		}
	}
	state := j.Update.NewState().PathMap()
	for p, f := range j.Files {
		if f.Src < 0 || f.Src >= len(j.Sources) {
			return fmt.Errorf("staged file %q has an invalid source index: %d", p, f.Src)
		}
		if !fs.ValidPath(f.Name) || f.Name == "." {
			return fmt.Errorf("staged file %q has an invalid path: %q", p, f.Name)
		}
		if !strings.EqualFold(state[p], f.Digest) {
			return fmt.Errorf("staged file %q has a different digest than the update", p)
		}
	}
	*s = Stage{root: j.Root, update: j.Update, sources: j.Sources, files: j.Files}
	return nil
}

// AddOption is a function that can be used to configure the behavior of
// [Stage.AddDir] or [Stage.AddFile]
type AddOption func(c *addConfig)

type addConfig struct {
	as       string
	noHidden bool
	remove   bool
	gos      int
}

func newAddConfig(opts ...AddOption) *addConfig {
	conf := &addConfig{}
	for _, o := range opts {
		o(conf)
	}
	return conf
}

// AddAs sets the logical name for staged content. When used with
// [Stage.AddDir], name is treated as a directory in which files from the
// source directory are added. For [Stage.AddFile], name is treated as a
// logical filename.
func AddAs(name string) AddOption {
	return func(c *addConfig) {
		c.as = name
	}
}

// AddAndRemove is an option for [Stage.AddDir] to also remove files in the
// stage that aren't included in the source directory. If used with [AddAs],
// only files under the directory named with [AddAs] are removed. This option
// is ignored if used with [Stage.AddFile].
func AddAndRemove() AddOption {
	return func(c *addConfig) {
		c.remove = true
	}
}

// AddWithoutHidden is an option for [Stage.AddDir] to exclude hidden files
// and directories from the source directory. This option is ignored if used
// with [Stage.AddFile].
func AddWithoutHidden() AddOption {
	return func(c *addConfig) {
		c.noHidden = true
	}
}

// AddDigestJobs is an option for [Stage.AddDir] that sets the number of
// goroutines used to check and digest files in the source directory. If num
// is less than 1, the number of CPUs is used.
func AddDigestJobs(num int) AddOption {
	return func(c *addConfig) {
		c.gos = num
	}
}

// stagedFile is the record of the file a logical path was added from.
type stagedFile struct {
	Src  int    `json:"src"`  // index of the directory in the stage's sources
	Name string `json:"name"` // slash-separated path relative to the directory
	statcache.Entry
}

// stageJSON is the saved form of a Stage.
type stageJSON struct {
	Root    string                `json:"root"`
	Update  *ocfl.ObjectUpdate    `json:"update"`
	Sources []string              `json:"sources"`
	Files   map[string]stagedFile `json:"files"`
}

// check checks the files names, relative to the directory dir, using gos
// goroutines, and returns their entries. Files that were added before from
// the same path are only read if they have changed. It returns an error if
// any file can't be read.
func (s *Stage) check(ctx context.Context, dir string, names []string, gos int) ([]statcache.Entry, error) {
	cached := map[string]*statcache.Entry{} // source path -> entry
	for _, f := range s.files {
		cached[s.sourcePath(f)] = &f.Entry
	}
	items := make([]statcache.Item, len(names))
	for i, name := range names {
		p := filepath.Join(dir, filepath.FromSlash(name))
		items[i] = statcache.Item{Path: p, Cached: cached[p]}
	}
	results, err := s.checker(gos).Check(ctx, items)
	if err != nil {
		return nil, err
	}
	var errs []error
	entries := make([]statcache.Entry, len(results))
	for i, r := range results {
		switch {
		case r.Err != nil:
			errs = append(errs, r.Err)
		case r.Status == statcache.StatusMissing:
			errs = append(errs, &fs.PathError{Op: "lstat", Path: items[i].Path, Err: fs.ErrNotExist})
		}
		entries[i] = r.Entry
	}
	if len(errs) > maxFileErrors {
		more := fmt.Errorf("and %d more errors", len(errs)-maxFileErrors)
		errs = append(errs[:maxFileErrors], more)
	}
	return entries, errors.Join(errs...)
}

// add adds files with the entries to u, which is s's update or a changed
// copy of it that replaces it, with logical names from dstName. names are
// relative to the directory dir. If there is an error, s is unchanged.
func (s *Stage) add(u *ocfl.ObjectUpdate, dir string, names []string, entries []statcache.Entry, dstName func(string) string, logger *slog.Logger) error {
	before := s.update.NewState().PathMap()
	updateEntries := make([]ocfl.UpdateEntry, len(names))
	for i, name := range names {
		updateEntries[i] = ocfl.UpdateEntry{Name: dstName(name), Digest: entries[i].Digest}
	}
	if err := u.AddAll(updateEntries); err != nil {
		return err
	}
	s.update = u
	src := slices.Index(s.sources, dir)
	if src < 0 {
		src = len(s.sources)
		s.sources = append(s.sources, dir)
	}
	if s.files == nil {
		s.files = map[string]stagedFile{}
	}
	for i, name := range names {
		s.files[dstName(name)] = stagedFile{Src: src, Name: name, Entry: entries[i]}
	}
	s.prune()
	logChanges(logger, before, s.update.NewState().PathMap())
	return nil
}

// cloneUpdate returns a copy of s's update.
func (s *Stage) cloneUpdate() (*ocfl.ObjectUpdate, error) {
	b, err := json.Marshal(s.update)
	if err != nil {
		return nil, err
	}
	var u ocfl.ObjectUpdate
	if err := json.Unmarshal(b, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// checker returns a checker for s's files that uses gos goroutines.
func (s *Stage) checker(gos int) *statcache.Checker {
	return &statcache.Checker{
		Alg:     s.update.DigestAlgorithm(),
		Compare: s.Compare,
		Workers: gos,
	}
}

// prune drops records of staged files for logical paths that aren't in the
// update's state with the same digest.
func (s *Stage) prune() {
	state := s.update.NewState().PathMap()
	maps.DeleteFunc(s.files, func(p string, f stagedFile) bool {
		return !strings.EqualFold(state[p], f.Digest)
	})
}

// sourcePath returns the path of the file f.
func (s *Stage) sourcePath(f stagedFile) string {
	return filepath.Join(s.sources[f.Src], filepath.FromSlash(f.Name))
}

// resolvePath returns the absolute path of name with symbolic links
// resolved, so that a link given as the file or directory to add is followed.
func resolvePath(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// listFiles returns the slash-separated paths of the files in the directory
// dir, relative to dir, in lexical order. If noHidden is true, files and
// directories with names starting with "." are skipped. Symbolic links are
// listed, not followed.
func listFiles(ctx context.Context, dir string, noHidden bool) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if noHidden && p != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return names, nil
}

// logChanges logs files that were added, updated or removed between the
// stage states before and after.
func logChanges(logger *slog.Logger, before, after ocfl.PathMap) {
	if logger == nil {
		return
	}
	for p := range before.SortedPaths() {
		if _, ok := after[p]; !ok {
			logger.Info("file removed", "path", p)
		}
	}
	for p, dig := range after.SortedPaths() {
		switch prev, ok := before[p]; {
		case !ok:
			logger.Info("file added", "path", p)
		case prev != dig:
			logger.Info("file updated", "path", p)
		}
	}
}

// hasDir returns true if state has files in the directory dir.
func hasDir(state ocfl.PathMap, dir string) bool {
	for p := range state {
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}
