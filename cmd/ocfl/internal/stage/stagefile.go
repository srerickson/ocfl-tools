package stage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"
)

// StageFile reprepresent a local stage file for building updates
// to OCFL ojects.
type StageFile struct {
	// Object ID
	ID string `json:"object_id"`

	// number of new object version
	NextHead ocfl.VNum `json:"next_head"`

	// State for new object versions
	NextState ocfl.PathMap `json:"next_state"`

	// digests that are already part of the object, don't need to be uploaded.
	ExistingDigests []string `json:"existing"`

	// Primary digest algorithm (sha512 or sha256)
	AlgID string `json:"digest_algorithm"`

	// LocalContent maps (primary) digest values to local files
	LocalContent map[string]*LocalFile `json:"local_content"`

	// Fixity Algorithms
	FixityIDs []string `json:"fixity_algorithms"`

	// Fixity maps digests to alternate digests (for new content)
	Fixity map[string]digest.Set `json:"fixity"`

	// optional logger
	logger *slog.Logger
}

// NewStageFile creates a new stage for the object. The newAlg argument can be used
// to set non-standard digest algorithm if the object does not exist.
func NewStageFile(obj *ocfl.Object, newAlg string) (*StageFile, error) {
	objID := obj.ID()
	if objID == "" {
		return nil, errors.New("object ID not set")
	}
	var stage = &StageFile{
		ID:              obj.ID(),
		NextHead:        ocfl.V(1),
		NextState:       ocfl.PathMap{},
		Fixity:          map[string]digest.Set{},
		ExistingDigests: []string{},
		LocalContent:    map[string]*LocalFile{},
		AlgID:           newAlg,
	}
	if obj.Exists() {
		next, err := obj.Head().Next()
		if err != nil {
			return nil, err
		}
		stage.NextHead = next
		stage.NextState = obj.Version(0).State().PathMap()
		stage.AlgID = obj.DigestAlgorithm().ID()
		stage.ExistingDigests = slices.Collect(maps.Keys(obj.Manifest()))
	}
	return stage, nil
}

func ReadStageFile(name string) (*StageFile, error) {
	var stage StageFile
	bytes, err := os.ReadFile(name)
	if err != nil {
		// have you created
		return nil, err
	}
	if err := json.Unmarshal(bytes, &stage); err != nil {
		return nil, err
	}
	return &stage, nil
}

// Algs returns the stage's digest algorithms as a slice. The primary algorithm
// is first, the rest are fixity.
func (s StageFile) Algs() ([]digest.Algorithm, error) {
	var err error
	algs := make([]digest.Algorithm, 1+len(s.FixityIDs))
	algs[0], err = digest.DefaultRegistry().Get(s.AlgID)
	if err != nil {
		return nil, err
	}
	for i, algID := range s.FixityIDs {
		algs[i+1], err = digest.DefaultRegistry().Get(algID)
		if err != nil {
			return nil, err
		}
	}
	return algs, nil
}

// AddFile digests a local file and adds it to the stage using the file's
// basename. Use the [AddAs] option to change the logical name for the staged
// file.
func (s *StageFile) AddFile(localPath string, opts ...AddOption) error {
	addConf := addConfig{}
	for _, o := range opts {
		o(&addConf)
	}
	if !filepath.IsAbs(localPath) {
		absPath, err := filepath.Abs(localPath)
		if err != nil {
			return err
		}
		localPath = absPath
	}
	if addConf.as == "" {
		addConf.as = filepath.Base(localPath)
	}
	if addConf.as == "." || !fs.ValidPath(addConf.as) {
		return fmt.Errorf("invalid file name: %s", addConf.as)
	}
	algs, err := s.Algs()
	if err != nil {
		return err
	}
	digester := digest.NewMultiDigester(algs...)
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", localPath)
	}
	if _, err := io.Copy(digester, f); err != nil {
		return err
	}
	localFile := &LocalFile{
		Path:    localPath,
		Size:    info.Size(),
		Modtime: info.ModTime(),
	}
	return s.add(addConf.as, localFile, digester.Sums())
}

// AddDir walks files in a localDir and generates digests for files using the
// stage's digest algorithm. By default hidden files are ignored. See
// [AddOption] functions for ways to customize this.
func (s *StageFile) AddDir(ctx context.Context, localDir string, opts ...AddOption) error {
	addConf := addConfig{}
	for _, o := range opts {
		o(&addConf)
	}
	if !filepath.IsAbs(localDir) {
		absLocalDir, err := filepath.Abs(localDir)
		if err != nil {
			return err
		}
		localDir = absLocalDir
	}
	if addConf.as == "" {
		addConf.as = "."
	}
	if !fs.ValidPath(addConf.as) {
		return fmt.Errorf("invalid directory name: %s", addConf.as)
	}
	localFS := ocflfs.DirFS(localDir)
	if addConf.gos < 1 {
		addConf.gos = runtime.NumCPU()
	}
	algs, err := s.Algs()
	if err != nil {
		return err
	}
	alg := algs[0]
	var fixity []digest.Algorithm
	if len(algs) > 1 {
		fixity = algs[1:]
	}
	if addConf.remove {
		// Remove files before adding to get rid of potential conflicting paths.
		// Files in new state (under 'as') that don't exist in localDir are removed.
		for p := range s.NextState {
			statName := p
			if addConf.as != "." {
				if !strings.HasPrefix(p, addConf.as+"/") {
					continue // ignore files that aren't inside 'as'
				}
				// stat name relative to 'as'
				statName = strings.TrimPrefix(p, addConf.as+"/")
			}
			_, err := ocflfs.StatFile(ctx, localFS, statName)
			if err == nil {
				continue
			}
			// Need the handle case where stateName parent is an existing file: this
			// isn't a 'not found' error. See: https://github.com/golang/go/issues/18974
			shouldRemove := errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
			if shouldRemove {
				delete(s.NextState, p)
				if s.logger != nil {
					s.logger.Info("file removed", "path", p)
				}
				continue
			}
			// other kinds of errors should be returned
			return err
		}
	}
	filesIter, walkErr := ocflfs.UntilErr(ocflfs.WalkFiles(ctx, localFS, "."))
	if !addConf.withHidden {
		filesIter = ocflfs.FilterFiles(filesIter, ocflfs.IsNotHidden)
	}
	for result, err := range digest.DigestFilesBatch(ctx, filesIter, addConf.gos, alg, fixity...) {
		if err != nil {
			return err
		}
		// convert result path back to os-specific path
		logicalPath := path.Join(addConf.as, result.FullPath())
		localPath := filepath.Join(localDir, filepath.FromSlash(result.FullPath()))
		localFile := &LocalFile{
			Path:    localPath,
			Size:    result.Info.Size(),
			Modtime: result.Info.ModTime(),
		}
		if err := s.add(logicalPath, localFile, result.Digests); err != nil {
			return err
		}
	}
	if err := walkErr(); err != nil {
		return err
	}
	return nil
}

// StateErrors return an iterator that yields non-nil errors in the stage state.
// This includes validation errors and digest with no associated content.
func (s StageFile) StateErrors() iter.Seq[error] {
	return func(yield func(error) bool) {
		if err := s.NextState.DigestMap().Valid(); err != nil {
			if !yield(err) {
				return
			}
		}
		for _, digest := range s.NextState {
			if s.LocalContent[digest] != nil {
				continue
			}
			if slices.Contains(s.ExistingDigests, digest) {
				continue
			}
			err := fmt.Errorf("stage state includes a digest with no associated content: %q", digest)
			if !yield(err) {
				return
			}
		}
	}
}

// ContentErrors returns an iterator that yields non-nil errors for local files
// referenced in the stage that are either no longer readable or have changed
// size or modtime.
func (s StageFile) ContentErrors() iter.Seq[error] {
	return func(yield func(error) bool) {
		for _, file := range s.LocalContent {
			name := file.Path
			info, err := os.Stat(name)
			if err != nil {
				err = fmt.Errorf("file is missing or unreadable: %w", err)
			}
			if err == nil && info.Size() != file.Size {
				err = fmt.Errorf("file has changed (size): %q", name)
			}
			if err == nil && info.ModTime().Compare(file.Modtime) != 0 {
				err = fmt.Errorf("file has changed (modtime): %q", name)
			}
			if err != nil {
				if !yield(err) {
					return
				}
			}
		}
	}
}

// Write s to file name as json
func (s StageFile) Write(name string) error {
	stageBytes, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.WriteFile(name, stageBytes, 0644); err != nil {
		return err
	}
	return nil
}

// stage implements ocfl.ContentSource
func (s StageFile) GetContent(digest string) (ocflfs.FS, string) {
	localFile := s.LocalContent[digest]
	if localFile == nil {
		return nil, ""
	}
	dir := filepath.Dir(localFile.Path)
	name := filepath.Base(localFile.Path)
	return ocflfs.DirFS(dir), name
}

// stage implements ocfl.FixitySource
func (s StageFile) GetFixity(digest string) digest.Set {
	return s.Fixity[digest]
}

// Write a list of filenames to the writer
func (s *StageFile) List(w io.Writer, withDigests bool) {
	for p, digest := range s.NextState.SortedPaths() {
		if withDigests {
			fmt.Fprintln(w, digest, p)
			continue
		}
		fmt.Fprintln(w, p)
	}
}

// Remove removes logicalPath from the stage state. If recursive is true,
// logicalPath is treated as a directory and all files under it are removed.
func (s *StageFile) Remove(logicalPath string, recursive bool) error {
	toDelete := path.Clean(logicalPath)
	switch {
	case recursive && toDelete == ".":
		// delete everything
		s.NextState = ocfl.PathMap{}
	default:
		for p := range s.NextState {
			recursiveMatch := recursive && (strings.HasPrefix(p, toDelete+"/"))
			if p == toDelete || recursiveMatch {
				delete(s.NextState, p)
				if s.logger != nil {
					s.logger.Info("file removed", "path", p)
				}
			}
		}
	}
	return nil
}

func (s StageFile) Stage() (*ocfl.Stage, error) {
	if err := errors.Join(slices.Collect(s.StateErrors())...); err != nil {
		return nil, err
	}
	if err := errors.Join(slices.Collect(s.ContentErrors())...); err != nil {
		return nil, err
	}
	algs, err := s.Algs()
	if err != nil {
		return nil, err
	}
	return &ocfl.Stage{
		State:           s.NextState.DigestMap(),
		DigestAlgorithm: algs[0],
		ContentSource:   s,
		FixitySource:    s,
	}, nil
}

func (s *StageFile) SetLogger(l *slog.Logger) {
	s.logger = l
}

// Add adds a digestsed file to the stage as logical path.
func (s *StageFile) add(logical string, local *LocalFile, digests digest.Set) error {
	prevDigest := s.NextState[logical]
	newDigest, fixity := digests.Split(s.AlgID)
	if prevDigest != newDigest {
		if conflict := pathConflict(s.NextState, logical); conflict != "" {
			err := fmt.Errorf("can't add %q because of conflict with %q", logical, conflict)
			return err
		}
		if s.logger != nil {
			action := "file added"
			if prevDigest != "" {
				action = "file updated"
			}
			s.logger.Info(action, "path", logical)
		}
		s.NextState[logical] = newDigest
	}
	if len(fixity) > 0 {
		s.Fixity[newDigest] = fixity
	}
	alreadyCommitted := slices.Contains(s.ExistingDigests, newDigest)
	_, alreadyStaged := s.LocalContent[newDigest]
	if !alreadyCommitted && !alreadyStaged {
		s.LocalContent[newDigest] = local
	}
	return nil
}

type LocalFile struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Modtime time.Time `json:"modtime"`
}

// AddOption is a function that can be used to configure the behavior of
// [AddDir] or [AddFile]
type AddOption func(c *addConfig)

type addConfig struct {
	as         string
	withHidden bool
	remove     bool
	gos        int
}

// AddAs sets the logical name for staged content. When used with [AddDir], name
// is treated as a directory in which files from the source directory are added.
// For [AddFile], name is treated as a logical filename.
func AddAs(name string) AddOption {
	return func(c *addConfig) {
		c.as = name
	}
}

// AddAndRemove is an option for [AddDir] to also remove files in the stage that
// aren't included in the source directory. If used with [AddAs], only files
// under the directory named with [AddAs] are removed. This option is ignored if
// used with [AddFile].
func AddAndRemove() AddOption {
	return func(c *addConfig) {
		c.remove = true
	}
}

// AddWithHidden is an option for [AddDir] to included hidden files and directories
// from the source directory. This options is ignored if used with [AddFile].
func AddWithHidden() AddOption {
	return func(c *addConfig) {
		c.withHidden = true
	}
}

// AddDigestJobs is an option for [AddDir] that sets the number of goroutines used
// to digest files in the source directory.
func AddDigestJobs(num int) AddOption {
	return func(c *addConfig) {
		c.gos = num
	}
}

// chek
// return any keys in state that would conflict with newName
func pathConflict(state ocfl.PathMap, newName string) string {
	for name := range state {
		if strings.HasPrefix(name, newName+"/") || strings.HasPrefix(newName, name+"/") {
			return name
		}
	}
	return ""
}
