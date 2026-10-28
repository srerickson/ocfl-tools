package run

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/statcache"
)

// StageCmd is 'ocfl stage': commands for preparing and committing object
// updates.
type StageCmd struct {
	stageCacheFlag `embed:""`

	Stage     string `name:"stage" short:"s" xor:"stage" help:"ID of a stage in the stage cache, or a unique prefix of it"`
	File      string `name:"file" short:"f" xor:"stage" help:"path to a stage file, used instead of the stage cache"`
	CheckStat string `name:"check-stat" enum:"default,minimal" default:"default" ocflenv:"OCFL_CHECK_STAT" help:"file metadata compared to find staged files that haven't changed: 'default' or 'minimal' (size, mtime in seconds, and mode; for network file systems)"`

	Add    StageAddCmd    `cmd:"" help:"Add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"Commit the stage as a new object version"`
	Delete StageDeleteCmd `cmd:"" help:"Delete a stage that hasn't been committed"`
	Diff   StageDiffCmd   `cmd:"" help:"Show changes between an upstream object or directory and the stage"`
	Ls     StageListCmd   `cmd:"" help:"List files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"Create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"Remove a file or directory from the stage"`
	Status StageStatusCmd `cmd:"" help:"Show stage details and report any errors"`
}

func (cmd *StageCmd) Help() string {
	return `A stage records changes to an object that can be committed as a new
version. Create one with 'stage new', change it with 'stage add' and 'stage
rm', and commit it with 'stage commit', which removes the stage. Staged
content is digested when it's added, but it isn't copied until the stage is
committed.

Stages are saved in the stage cache directory (--stage-cache), which has at
most one stage for each object in a storage root. 'stage new' prints the new
stage's ID, and the other commands take the ID, or a unique prefix of it,
with -s. To save a stage somewhere else, use -f with a file path instead.
Only 'stage new' uses --root: the other commands read the storage root from
the stage.

    ocfl stage new --id my-object
    ocfl stage add -s <stage-id> my-files/
    ocfl stage rm -s <stage-id> old-file.txt
    ocfl stage commit -s <stage-id> -m "update data" -n "My Name" -e me@example.net

The stage file records each staged file's metadata (size, modification
time, inode, and so on), like git's index. 'stage status' and 'stage add'
only read staged files whose metadata has changed. On network file systems
(SMB/CIFS, NFS), where the inode and change time aren't stable, use
--check-stat=minimal to compare only the size, modification time in whole
seconds, and mode.

If a commit is interrupted, the stage is kept. Run 'stage commit --resume'
to finish the commit, or 'stage commit --revert' to undo the partial update
and return the stage to its uncommitted state. 'stage delete' deletes a
stage that hasn't been committed.`
}

// ref returns the stage file named by --stage or --file. It returns an error
// if neither is set, or if --root was given on the command line: commands
// other than 'stage new' use the storage root saved in the stage.
func (cmd *StageCmd) ref(env *cmdEnv, kctx *kong.Context) (stageRef, error) {
	if flagGiven(kctx, "root") {
		return stageRef{}, errors.New("--root can only be used with 'stage new': the storage root is read from the stage")
	}
	switch {
	case cmd.File != "":
		return fileRef(cmd.File)
	case cmd.Stage != "":
		cache, err := cmd.cache(env)
		if err != nil {
			return stageRef{}, err
		}
		id, err := cache.Find(cmd.Stage)
		if err != nil {
			return stageRef{}, err
		}
		return cmd.cacheRef(kctx, cache, id)
	}
	return stageRef{}, errors.New("a stage is required: use --stage (-s) with a stage ID or --file (-f) with a stage file")
}

// load opens the stage file named by --stage or --file, and returns it with
// the flags that name it.
func (cmd *StageCmd) load(env *cmdEnv, kctx *kong.Context) (*stage.File, stageRef, error) {
	ref, err := cmd.ref(env, kctx)
	if err != nil {
		return nil, stageRef{}, err
	}
	f, err := stage.Open(ref.path)
	if err != nil {
		return nil, stageRef{}, err
	}
	if cmd.CheckStat == "minimal" {
		f.Stage().Compare = statcache.CompareMinimal
	}
	return f, ref, nil
}

// loadDraft is like load, but it returns an error if the stage is frozen for
// a commit, which must be resumed or reverted before the stage can be
// changed.
func (cmd *StageCmd) loadDraft(env *cmdEnv, kctx *kong.Context) (*stage.File, stageRef, error) {
	f, ref, err := cmd.load(env, kctx)
	if err != nil {
		return nil, stageRef{}, err
	}
	if f.Stage().Frozen() {
		return nil, stageRef{}, fmt.Errorf("the stage can't be changed: %w", inProgressError(f.Stage(), ref))
	}
	return f, ref, nil
}

// NewStageCmd is 'ocfl stage new'.
type NewStageCmd struct {
	Alg string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID  string `name:"id" short:"i" required:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Help() string {
	return `Creates a stage for the object in the storage root (--root) and prints its
stage ID. The stage is saved in the stage cache, or in the file given with
-f. There can only be one stage for an object in the stage cache: if one
exists, commit or delete it first.`
}

func (cmd *NewStageCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	if parent.Stage != "" {
		return errors.New("--stage (-s) can't be used with 'stage new': the stage ID is set from the storage root and object ID")
	}
	alg, err := digest.DefaultRegistry().Get(cmd.Alg)
	if err != nil {
		return err
	}
	root, rootLoc, err := env.getRootLocation(ctx)
	if err != nil {
		return err
	}
	var ref stageRef
	if parent.File != "" {
		ref, err = fileRef(parent.File)
	} else {
		ref, err = parent.objectRef(env, kctx, rootLoc, cmd.ID)
	}
	if err != nil {
		return err
	}
	// fail before reading the object if the stage exists
	if err := ref.checkNew(); err != nil {
		return err
	}
	s, err := newStage(ctx, root, rootLoc, cmd.ID, alg)
	if err != nil {
		return err
	}
	if _, err := ref.create(s); err != nil {
		return err
	}
	if ref.shortID == "" {
		env.logger.Info("stage file created", "path", ref.path, "object_id", s.ID(), "object_version", s.NextHead())
		return nil
	}
	env.logger.Info("stage created", "stage", ref.shortID, "object_id", s.ID(), "object_version", s.NextHead(), "path", ref.path)
	fmt.Fprintln(env.stdout, ref.shortID)
	return nil
}

// StageAddCmd is 'ocfl stage add'.
type StageAddCmd struct {
	NoHidden bool   `name:"no-hidden" help:"exclude hidden files and directories (.*). Ignored if path is a file."`
	As       string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Jobs     int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to the number of CPU cores."`
	Remove   bool   `name:"remove" help:"also remove staged files not found in the path. Ignored if path is a file."`
	Path     string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, _, err := parent.loadDraft(env, kctx)
	if err != nil {
		return err
	}
	s := f.Stage()
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	ftype := info.Mode().Type()
	switch {
	case ftype.IsDir():
		opts := []stage.AddOption{
			stage.AddAs(cmd.As),
			stage.AddDigestJobs(cmd.Jobs),
		}
		if cmd.Remove {
			opts = append(opts, stage.AddAndRemove())
		}
		if cmd.NoHidden {
			opts = append(opts, stage.AddWithoutHidden())
		}
		if err := s.AddDir(ctx, absPath, env.logger, opts...); err != nil {
			return err
		}
	case ftype.IsRegular():
		if err := s.AddFile(ctx, absPath, env.logger, stage.AddAs(cmd.As)); err != nil {
			return err
		}
	default:
		return errors.New("unsupported file type for: " + absPath)
	}
	return f.Save()
}

// StageCommitCmd is 'ocfl stage commit'.
type StageCommitCmd struct {
	Message string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name    string `name:"name" short:"n" ocflenv:"OCFL_USER_NAME" help:"Username to include in the object version metadata"`
	Email   string `name:"email" short:"e" ocflenv:"OCFL_USER_EMAIL" help:"User email to include in the object version metadata"`
	Resume  bool   `name:"resume" xor:"mode" help:"Finish an interrupted commit using the saved version metadata"`
	Revert  bool   `name:"revert" xor:"mode" help:"Undo an interrupted commit and return the stage to its uncommitted state"`
}

func (cmd *StageCommitCmd) Help() string {
	return `Commits the stage as a new object version. The version metadata (-m, -n
and -e) is saved in the stage file before the object is changed. If the
commit is interrupted or fails, the stage file is kept: run the command
with --resume to finish the commit using the saved values, or with --revert
to undo the partial update. -m, -n and -e can't be used with --resume or
--revert. After a revert, the stage can be changed and committed again, or
deleted. The commit is to the storage root saved in the stage.`
}

func (cmd *StageCommitCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, ref, err := parent.load(env, kctx)
	if err != nil {
		return err
	}
	s := f.Stage()
	root, err := env.openRoot(ctx, s.Root())
	if err != nil {
		return err
	}
	switch {
	case cmd.Revert:
		return cmd.revert(ctx, env, root, f, ref, kctx)
	case cmd.Resume:
		return cmd.resume(ctx, env, root, f, ref, kctx)
	case s.Frozen():
		return inProgressError(s, ref)
	}
	if err := checkWritable(root); err != nil {
		return err
	}
	err = f.Commit(ctx, root, cmd.Message, newUser(cmd.Name, cmd.Email), env.logger)
	var notSaved *stage.DraftNotSavedError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &notSaved):
		return draftNotSavedError(s, ref, notSaved)
	case s.Frozen():
		return commitError(root, s, ref, err)
	case errors.Is(err, ocfl.ErrUpdateConflict):
		return conflictError(s, err, "  the stage can't be committed: delete it and create a new one",
			"  to delete it: "+ref.command("delete"))
	case errors.Is(err, ocfl.ErrMissingContent):
		return fmt.Errorf("the stage can't be committed: %w\n  add the files again or remove them from the stage", err)
	}
	return err
}

// resume finishes the interrupted commit of the frozen stage in f, which ref
// names, using the saved version metadata.
func (cmd *StageCommitCmd) resume(ctx context.Context, env *cmdEnv, root *ocfl.Root, f *stage.File, ref stageRef, kctx *kong.Context) error {
	if err := checkNoVersionFlags(kctx, "--resume"); err != nil {
		return err
	}
	err := f.Resume(ctx, root, env.logger)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, stage.ErrNotFrozen):
		return fmt.Errorf("%w: nothing to resume", err)
	}
	return commitError(root, f.Stage(), ref, err)
}

// revert reverts the interrupted commit of the frozen stage in f, which ref
// names, and saves the stage as a draft.
func (cmd *StageCommitCmd) revert(ctx context.Context, env *cmdEnv, root *ocfl.Root, f *stage.File, ref stageRef, kctx *kong.Context) error {
	if err := checkNoVersionFlags(kctx, "--revert"); err != nil {
		return err
	}
	s := f.Stage()
	err := f.Revert(ctx, root, env.logger)
	switch {
	case errors.Is(err, stage.ErrNotFrozen):
		return fmt.Errorf("%w: nothing to revert", err)
	case err != nil && !s.Frozen():
		// the commit was reverted, but the stage file still has the frozen
		// stage: resuming it would apply the commit again
		return fmt.Errorf("revert of %s (%s): %w\n  the stage file still has the commit: don't resume it\n  to finish reverting: %s",
			s.ID(), s.NextHead(), err, ref.command("commit", "--revert"))
	case errors.Is(err, ocfl.ErrUpdateCompleted):
		return fmt.Errorf("the commit of %s (%s) is complete and can't be reverted; to finish the commit and remove the stage file, run:\n  %s",
			s.ID(), s.NextHead(), ref.command("commit", "--resume"))
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("revert of %s (%s) was interrupted\n  to finish reverting: %s",
			s.ID(), s.NextHead(), ref.command("commit", "--revert"))
	case errors.Is(err, ocfl.ErrUpdateConflict):
		return fmt.Errorf("the commit of %s (%s) can't be reverted: %w\n  object path: %s\n  check the object with 'ocfl validate', then delete the stage: %s",
			s.ID(), s.NextHead(), err, objectLocation(root, s.ID()), ref.command("delete", "--force"))
	case err != nil:
		return fmt.Errorf("reverting the commit of %s (%s): %w", s.ID(), s.NextHead(), err)
	}
	env.logger.Info("object update reverted: the stage can be changed and committed again, or deleted",
		"path", ref.path, "delete", ref.command("delete"))
	return nil
}

// StageDeleteCmd is 'ocfl stage delete'.
type StageDeleteCmd struct {
	Force bool `name:"force" help:"Delete the stage even if a commit is in progress. The commit can't be resumed or reverted afterwards."`
}

func (cmd *StageDeleteCmd) Help() string {
	return `Deletes a stage that hasn't been committed. A stage with a commit in
progress can't be deleted: resume or revert the commit first. If neither is
possible (for example, because the object was changed by something else),
use --force. The object may be left partially updated: check it with 'ocfl
validate'.`
}

func (cmd *StageDeleteCmd) Run(env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, ref, err := parent.load(env, kctx)
	if err != nil {
		return err
	}
	s := f.Stage()
	if cmd.Force {
		err = f.Discard()
	} else {
		err = f.Delete()
	}
	switch {
	case errors.Is(err, stage.ErrFrozen):
		return fmt.Errorf("the stage can't be deleted: %w", inProgressError(s, ref))
	case err != nil:
		return err
	}
	if s.Frozen() {
		env.logger.Warn("deleted a stage with a commit in progress: the object may be partially updated",
			"object_id", s.ID(), "object_version", s.NextHead(), "storage_root", s.Root())
		return nil
	}
	env.logger.Info("stage deleted", "object_id", s.ID(), "path", ref.path)
	return nil
}

// StageDiffCmd is 'ocfl stage diff'.
type StageDiffCmd struct {
	Dir string `name:"dir" help:"use a local directory rather than upstream object as basis for comparison to the stage."`
}

func (cmd *StageDiffCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, _, err := parent.load(env, kctx)
	if err != nil {
		return err
	}
	s := f.Stage()
	baseState := s.Update().BaseState().PathMap()
	if cmd.Dir != "" {
		baseState = ocfl.PathMap{}
		alg := s.DigestAlgorithm()
		filesIter, walkErr := ocflfs.UntilErr(ocflfs.WalkFiles(ctx, ocflfs.DirFS(cmd.Dir), "."))
		for result, err := range digest.DigestFiles(ctx, filesIter, alg) {
			if err != nil {
				return err
			}
			baseState[result.FullPath()] = result.Digests[alg.ID()]
		}
		if err := walkErr(); err != nil {
			return err
		}
	}
	diffs, err := diff.Diff(baseState, s.Update().NewState().PathMap())
	if err != nil {
		return err
	}
	if diffs.Empty() {
		return nil
	}
	fmt.Fprint(env.stdout, diffs.String())
	return nil
}

// StageListCmd is 'ocfl stage ls'.
type StageListCmd struct {
	WithDigests bool `name:"digests" short:"d" help:"include file digests in output"`
}

func (cmd *StageListCmd) Run(env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, _, err := parent.load(env, kctx)
	if err != nil {
		return err
	}
	f.Stage().List(env.stdout, cmd.WithDigests)
	return nil
}

// StageRmCmd is 'ocfl stage rm'.
type StageRmCmd struct {
	Recursive bool   `name:"recursive" short:"r" help:"remove all files in the directory"`
	Path      string `arg:"" name:"path" help:"file or directory to remove"`
}

func (cmd *StageRmCmd) Run(env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, _, err := parent.loadDraft(env, kctx)
	if err != nil {
		return err
	}
	if err := f.Stage().Remove(cmd.Path, cmd.Recursive, env.logger); err != nil {
		return err
	}
	return f.Save()
}

// StageStatusCmd is 'ocfl stage status'.
type StageStatusCmd struct{}

func (cmd *StageStatusCmd) Help() string {
	return `Shows the object and version the stage is for, whether it has changes to
commit, and any staged files that have changed since they were added, like
'git status'. Staged files are only read if their metadata has changed
since they were added (see 'ocfl stage --help'). If a staged file has
changed and its content is needed for the commit, status exits with an
error.`
}

func (cmd *StageStatusCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	f, ref, err := parent.load(env, kctx)
	if err != nil {
		return err
	}
	s := f.Stage()
	upd := s.Update()
	fmt.Fprintf(env.stdout, "storage root: %s\n", s.Root())
	fmt.Fprintf(env.stdout, "object:       %s (%s)\n", s.ID(), s.NextHead())
	fmt.Fprintf(env.stdout, "digest alg:   %s\n", s.DigestAlgorithm().ID())
	fmt.Fprintf(env.stdout, "state size:   %d files\n", len(upd.NewState().PathMap()))
	stateDiff, err := diff.Diff(upd.BaseState().PathMap(), upd.NewState().PathMap())
	if err != nil {
		return err
	}
	switch {
	case s.Frozen():
		fmt.Fprintf(env.stdout, "commit in progress: %s\n", s.NextHead())
		fmt.Fprintf(env.stdout, "  to resume: %s\n", ref.command("commit", "--resume"))
		fmt.Fprintf(env.stdout, "  to revert: %s\n", ref.command("commit", "--revert"))
	case !stateDiff.Empty():
		fmt.Fprintln(env.stdout, "stage has changes to commit")
	default:
		fmt.Fprintln(env.stdout, "stage is unchanged and/or empty")
	}
	return checkStagedFiles(ctx, env, f)
}

// stageCacheFlag is the --stage-cache flag of commands that save stages in the
// stage cache.
type stageCacheFlag struct {
	StageCache string `name:"stage-cache" ocflenv:"OCFL_STAGE_CACHE" help:"directory for saved stages. Default: 'ocfl-tools/stage' in the user's cache directory"`
}

// cache returns the stage cache: the --stage-cache directory, or by default,
// 'ocfl-tools/stage' in the user's cache directory.
func (f stageCacheFlag) cache(env *cmdEnv) (stage.Cache, error) {
	dir := f.StageCache
	if dir == "" {
		base, err := env.userCacheDir()
		if err != nil {
			return stage.Cache{}, fmt.Errorf("finding the default stage cache: %w; set --stage-cache or $OCFL_STAGE_CACHE", err)
		}
		dir = filepath.Join(base, "ocfl-tools", "stage")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return stage.Cache{}, err
	}
	return stage.Cache{Dir: abs}, nil
}

// objectRef returns the stage file in the stage cache for the object id in the
// storage root at the location rootLoc.
func (f stageCacheFlag) objectRef(env *cmdEnv, kctx *kong.Context, rootLoc, id string) (stageRef, error) {
	cache, err := f.cache(env)
	if err != nil {
		return stageRef{}, err
	}
	return f.cacheRef(kctx, cache, stage.ID(rootLoc, id))
}

// cacheRef returns the stage file for the stage ID id in cache. The stage is
// named by a short ID in commands, with --stage-cache if it was given on the
// command line.
func (f stageCacheFlag) cacheRef(kctx *kong.Context, cache stage.Cache, id string) (stageRef, error) {
	short, err := cache.ShortID(id)
	if err != nil {
		return stageRef{}, err
	}
	var flags []string
	if flagGiven(kctx, "stage-cache") {
		flags = append(flags, "--stage-cache", cache.Dir)
	}
	flags = append(flags, "-s", short)
	return stageRef{path: cache.Path(id), flags: flags, cache: cache, id: id, shortID: short}, nil
}

// existingStageError returns the error for creating a stage when the stage
// file ref already exists.
func existingStageError(ref stageRef) error {
	f, err := stage.Open(ref.path)
	if err != nil {
		return fmt.Errorf("stage file already exists, but it can't be read: %w\n  to start over, remove the stage file: %s", err, ref.path)
	}
	s := f.Stage()
	if s.Frozen() {
		return fmt.Errorf("a stage for %s already exists: %w", s.ID(), inProgressError(s, ref))
	}
	return fmt.Errorf("a stage for %s already exists: %s\n  to see it: %s\n  to delete it: %s",
		s.ID(), ref.path, ref.command("status"), ref.command("delete"))
}

// stageRef is a stage file and the flags that name it in 'ocfl stage'
// commands.
type stageRef struct {
	path  string
	flags []string

	// if the stage file is in the stage cache:
	cache   stage.Cache
	id      string // stage ID
	shortID string // the stage ID's shortest unique prefix, used in commands
}

// command returns the command line for the 'ocfl stage' subcommand sub, using
// r's stage file, with args.
func (r stageRef) command(sub string, args ...string) string {
	cmd := append([]string{"ocfl", "stage", sub}, r.flags...)
	cmd = append(cmd, args...)
	for i, arg := range cmd {
		cmd[i] = shellQuote(arg)
	}
	return strings.Join(cmd, " ")
}

// checkNew returns an error if r's stage file exists, so creating a stage can
// fail before doing any work.
func (r stageRef) checkNew() error {
	if _, err := os.Lstat(r.path); err == nil {
		return existingStageError(r)
	}
	return nil
}

// create saves s in r's stage file, which must not exist: if it does, the
// error says how to see or delete the existing stage.
func (r stageRef) create(s *stage.Stage) (*stage.File, error) {
	var f *stage.File
	var err error
	if r.id != "" {
		f, err = r.cache.Create(r.id, s)
	} else {
		f, err = stage.Create(r.path, s)
	}
	if errors.Is(err, fs.ErrExist) {
		return nil, existingStageError(r)
	}
	return f, err
}

// fileRef returns the stage file name, which is named with -f and its absolute
// path in commands.
func fileRef(name string) (stageRef, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return stageRef{}, err
	}
	return stageRef{path: abs, flags: []string{"-f", abs}}, nil
}

// inProgressError returns an error saying that the commit of the frozen
// stage s, saved in the stage file ref, is in progress, and how to resume or
// revert it.
func inProgressError(s *stage.Stage, ref stageRef) error {
	return fmt.Errorf("a commit of %s (%s) is in progress\n  to resume: %s\n  to revert: %s",
		s.ID(), s.NextHead(), ref.command("commit", "--resume"), ref.command("commit", "--revert"))
}

// checkNoVersionFlags returns an error if -m, -n or -e were given on the
// command line. mode is the flag (--resume or --revert) that can't be used
// with them.
func checkNoVersionFlags(kctx *kong.Context, mode string) error {
	// -n and -e may be set from the environment: only flags on the command
	// line are an error.
	for _, flag := range []string{"message", "name", "email"} {
		if flagGiven(kctx, flag) {
			return fmt.Errorf("%s can't be used with --message (-m), --name (-n) or --email (-e)", mode)
		}
	}
	return nil
}

// checkStagedFiles reports staged files in the stage in f that have changed
// since they were added. It returns an error if the commit needs content that
// is only in changed files.
func checkStagedFiles(ctx context.Context, env *cmdEnv, f *stage.File) error {
	s := f.Stage()
	result, err := f.Check(ctx, env.logger)
	if err != nil {
		return err
	}
	if len(result.Changed) > 0 {
		fmt.Fprintln(env.stdout, "staged files changed since they were added:")
		if !s.Frozen() {
			fmt.Fprintln(env.stdout, `  (use "ocfl stage add" to stage the new content)`)
		}
		for _, c := range result.Changed {
			if c.Err != nil {
				fmt.Fprintf(env.stdout, "  error:    %s: %v\n", c.Path, c.Err)
				continue
			}
			fmt.Fprintf(env.stdout, "  %-9s %s (%s)\n", c.Status.String()+":", c.Path, c.Source)
		}
	}
	if len(result.Unavailable) > 0 {
		todo := "add the files again or remove them from the stage"
		if s.Frozen() {
			todo = "restore the files to resume the commit, or revert it"
		}
		return fmt.Errorf("the stage can't be committed: %w\n  %s", &stage.UnavailableError{Paths: result.Unavailable}, todo)
	}
	return nil
}

// checkWritable returns an error if root's storage can't be written, so a
// commit fails before its stage is frozen.
func checkWritable(root *ocfl.Root) error {
	if _, ok := root.FS().(ocflfs.WriteFS); !ok {
		return fmt.Errorf("storage root %s is read-only", locationString(root.FS(), root.Path()))
	}
	return nil
}

func newUser(name string, email string) ocfl.User {
	if email != "" && !strings.HasPrefix(email, "email:") {
		email = "email:" + email
	}
	return ocfl.User{Name: name, Address: email}
}

// newStage starts a stage to the object id in root, which is at the location
// rootLoc. alg is used if the object doesn't exist.
func newStage(ctx context.Context, root *ocfl.Root, rootLoc string, id string, alg digest.Algorithm) (*stage.Stage, error) {
	s, err := stage.New(ctx, root, rootLoc, id, alg)
	if errors.Is(err, ocfl.ErrObjectIncomplete) {
		return nil, fmt.Errorf("%w\nif a commit to the object was interrupted, resume or revert it using its stage file", err)
	}
	return s, err
}

// commitError describes the error err from committing the stage s, or
// resuming its commit, when s is frozen afterwards: the commit is in progress,
// and the error says how to resume or revert it. ref names s's stage file in
// commands.
func commitError(root *ocfl.Root, s *stage.Stage, ref stageRef, err error) error {
	commit := fmt.Sprintf("commit of %s (%s)", s.ID(), s.NextHead())
	objPath := "  object path: " + objectLocation(root, s.ID())
	resume := ref.command("commit", "--resume")
	revert := "  to revert: " + ref.command("commit", "--revert")
	var lines []string
	switch {
	case errors.Is(err, stage.ErrNotRemoved):
		lines = []string{commit + ": " + err.Error(), "  to remove it: " + resume}
	case errors.Is(err, context.Canceled):
		lines = []string{commit + " was interrupted; the object may be partially updated",
			objPath, "  to resume: " + resume, revert}
	case errors.Is(err, ocfl.ErrUpdateConflict):
		// the commit was resumed, and the object was changed by something
		// else after it was interrupted: the stage can't be resumed or
		// reverted. Conflicts committing a draft stage are described by
		// conflictError.
		lines = []string{commit + " can't be resumed or reverted: " + err.Error(),
			objPath, "  check the object with 'ocfl validate', then delete the stage: " + ref.command("delete", "--force")}
	case errors.Is(err, ocfl.ErrContentChanged), errors.Is(err, ocfl.ErrMissingContent):
		// Apply finds changed content as it copies it, after other content
		// may have been written, and a resumed commit may have been
		// interrupted after changing the object.
		lines = []string{commit + " failed; the object may be partially updated: " + err.Error(),
			objPath, "  to resume after restoring the staged files: " + resume, revert}
	default:
		lines = []string{commit + " failed; the object may be partially updated: " + err.Error(),
			objPath, "  to resume: " + resume, revert}
	}
	return &wrappedError{msg: strings.Join(lines, "\n"), err: err}
}

// conflictError describes the error err from committing the stage s, which
// failed before the object was changed because the object has changed since
// the stage was created. then is what to do next.
func conflictError(s *stage.Stage, err error, then ...string) error {
	lines := []string{fmt.Sprintf("commit of %s (%s) failed before the object was changed: %v", s.ID(), s.NextHead(), err)}
	lines = append(lines, then...)
	return &wrappedError{msg: strings.Join(lines, "\n"), err: err}
}

// draftNotSavedError describes the error err from committing the stage s,
// which failed before the object was changed because the object has changed
// since the stage was created, but couldn't be saved as a draft again. ref
// names s's stage file, which still has the frozen stage.
func draftNotSavedError(s *stage.Stage, ref stageRef, err *stage.DraftNotSavedError) error {
	return conflictError(s, err.Err, "  the stage file couldn't be restored: "+err.SaveErr.Error(),
		"  to delete it: "+ref.command("delete", "--force"))
}

// objectLocation returns the storage location of the object id in root, for
// messages.
func objectLocation(root *ocfl.Root, id string) string {
	objPath, err := root.ResolveID(id)
	if err != nil {
		return "unknown: " + err.Error()
	}
	return locationString(root.FS(), path.Join(root.Path(), objPath))
}
