package run

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/alecthomas/kong"
	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"
	ocflfs "github.com/srerickson/ocfl-go/fs"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/statcache"
)

// interruptedCommitHint is the hint for an object directory with content but
// no complete object.
const interruptedCommitHint = "if a commit to the object was interrupted, resume or revert it using its stage file"

// StageCmd is 'ocfl stage': commands for preparing and committing object
// updates.
type StageCmd struct {
	stageCacheFlag `embed:""`

	Stage     string `name:"stage" short:"s" xor:"stage" help:"ID of a stage in the stage cache, or a unique prefix of it"`
	File      string `name:"file" short:"f" xor:"stage" help:"path to a stage file, used instead of the stage cache"`
	CheckStat string `name:"check-stat" enum:"default,minimal" default:"default" ocflenv:"OCFL_CHECK_STAT" help:"file metadata compared to find staged files that haven't changed: 'default' or 'minimal' (size, mtime in seconds, and mode; for network file systems)"`

	Add    StageAddCmd    `cmd:"" help:"Add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"Commit the stage as a new object version"`
	Delete StageDeleteCmd `cmd:"" help:"Delete a stage that hasn't been committed, or clear its lock"`
	Diff   StageDiffCmd   `cmd:"" help:"Show changes between an upstream object or directory and the stage"`
	Ls     StageListCmd   `cmd:"" help:"List files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"Create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"Remove a file or directory from the stage"`
	Status StageStatusCmd `cmd:"" help:"Show stage details and report any errors"`
	Stages StageStagesCmd `cmd:"" default:"1" hidden:"" help:"List stages in the stage cache"`
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

Run 'ocfl stage' with no sub-command to list the stages in the stage cache,
with their short IDs, object IDs, and storage roots.

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
stage that hasn't been committed.

Commands that change a stage lock its stage file with a lock file (the stage
file's name with '.lock' appended) while they run, so only one of them can
change it at a time. If a command is killed, the lock file may be left
behind, and the commands fail because the stage is locked. If no other ocfl
command is using the stage, clear the lock with 'stage delete --lock'.`
}

// ref returns the stage file named by --stage or --file. It returns an error
// if neither is set, or if --root was given on the command line: commands
// other than 'stage new' use the storage root saved in the stage.
func (cmd *StageCmd) ref(env *cmdEnv, kctx *kong.Context) (stageRef, error) {
	return cmd.findRef(env, kctx, stage.Cache.Find)
}

// lockRef is like ref, but with --stage, it also finds a stage that has a lock
// file but no stage file.
func (cmd *StageCmd) lockRef(env *cmdEnv, kctx *kong.Context) (stageRef, error) {
	return cmd.findRef(env, kctx, stage.Cache.FindLock)
}

// findRef is ref, using find to find the stage ID given with --stage in the
// stage cache.
func (cmd *StageCmd) findRef(env *cmdEnv, kctx *kong.Context, find func(stage.Cache, string) (string, error)) (stageRef, error) {
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
		id, err := find(cache, cmd.Stage)
		if err != nil {
			return stageRef{}, err
		}
		return cmd.cacheRef(kctx, cache, id)
	}
	return stageRef{}, errors.New("a stage is required: use --stage (-s) with a stage ID or --file (-f) with a stage file")
}

// load opens the stage file named by --stage or --file without locking it,
// and returns it with the flags that name it. The stage file can be read and
// checked, but not changed: use loadLocked for that.
func (cmd *StageCmd) load(env *cmdEnv, kctx *kong.Context) (*stage.File, stageRef, error) {
	return cmd.open(env, kctx, stage.Open)
}

// loadLocked is like load, but it locks the stage file, so it can be changed.
// The caller must close the file to release the lock.
func (cmd *StageCmd) loadLocked(env *cmdEnv, kctx *kong.Context) (*stage.File, stageRef, error) {
	return cmd.open(env, kctx, stage.OpenLocked)
}

// open opens the stage file named by --stage or --file with openFn.
func (cmd *StageCmd) open(env *cmdEnv, kctx *kong.Context, openFn func(string) (*stage.File, error)) (*stage.File, stageRef, error) {
	ref, err := cmd.ref(env, kctx)
	if err != nil {
		return nil, stageRef{}, err
	}
	f, err := openFn(ref.path)
	if err != nil {
		return nil, stageRef{}, lockError(err, ref)
	}
	if cmd.CheckStat == "minimal" {
		f.Stage().Compare = statcache.CompareMinimal
	}
	return f, ref, nil
}

// loadDraft is like loadLocked, but it returns an error if the stage is
// frozen for a commit, which must be resumed or reverted before the stage can
// be changed.
func (cmd *StageCmd) loadDraft(env *cmdEnv, kctx *kong.Context) (*stage.File, stageRef, error) {
	f, ref, err := cmd.loadLocked(env, kctx)
	if err != nil {
		return nil, stageRef{}, err
	}
	if f.Stage().Frozen() {
		err := fmt.Errorf("the stage can't be changed: %w", inProgressError(f.Stage(), ref))
		closeStage(env, f, ref, "", &err)
		return nil, stageRef{}, err
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

func (cmd *NewStageCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) (err error) {
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
	f, err := ref.create(s)
	if err != nil {
		return err
	}
	defer closeStage(env, f, ref, "", &err)
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

func (cmd *StageAddCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) (err error) {
	f, ref, err := parent.loadDraft(env, kctx)
	if err != nil {
		return err
	}
	defer closeStage(env, f, ref, "", &err)
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

func (cmd *StageCommitCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) (err error) {
	f, ref, err := parent.loadLocked(env, kctx)
	if err != nil {
		return err
	}
	done := "the commit is complete"
	if cmd.Revert {
		done = ""
	}
	defer closeStage(env, f, ref, done, &err)
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
		return conflictError(s, err, staleDraftGuidance(ref)...)
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
	Force bool `name:"force" xor:"delete-mode" help:"Delete the stage even if a commit is in progress. The commit can't be resumed or reverted afterwards."`
	Lock  bool `name:"lock" xor:"delete-mode" help:"Only remove the stage's lock file, left behind by a command that was killed, and keep the stage."`
}

func (cmd *StageDeleteCmd) Help() string {
	return `Deletes a stage that hasn't been committed. A stage with a commit in
progress can't be deleted: resume or revert the commit first. If neither is
possible (for example, because the object was changed by something else),
use --force. The object may be left partially updated: check it with 'ocfl
validate'.

With --lock, the stage isn't deleted: only its lock file (the stage file's
name with '.lock' appended) is removed, and what it recorded about the
command that held the lock is printed. Use it to clear a lock left behind by
a command that was killed, when commands that change the stage fail because
it's locked. Removing the lock of a command that's still running lets another
command change the stage at the same time, so check that no other ocfl
command is using the stage first. (If it was, it stops before it next
changes the stage or the object, and fails, saying that its lock was
removed. If it had already finished with the stage, such as by committing
it, it only warns.) --lock works even if the stage file doesn't exist or
can't be read, and it can't be used with --force.`
}

func (cmd *StageDeleteCmd) Run(env *cmdEnv, parent *StageCmd, kctx *kong.Context) (err error) {
	if cmd.Lock {
		return cmd.removeLock(env, parent, kctx)
	}
	f, ref, err := parent.loadLocked(env, kctx)
	if err != nil {
		return err
	}
	defer closeStage(env, f, ref, "the stage was deleted", &err)
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

// removeLock removes the lock file of the stage named by --stage or --file.
func (cmd *StageDeleteCmd) removeLock(env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	ref, err := parent.lockRef(env, kctx)
	if err != nil {
		return err
	}
	info, err := stage.RemoveLock(ref.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(env.stdout, "the stage isn't locked: there's no lock file %s\n", info.Path)
		return nil
	case err != nil:
		return err
	}
	holder := info.Holder
	if holder == "" {
		holder = "unknown process"
	}
	fmt.Fprintf(env.stdout, "removed lock file %s (held by %s)\n", info.Path, holder)
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

func (cmd *StageRmCmd) Run(env *cmdEnv, parent *StageCmd, kctx *kong.Context) (err error) {
	f, ref, err := parent.loadDraft(env, kctx)
	if err != nil {
		return err
	}
	defer closeStage(env, f, ref, "", &err)
	if err := f.Stage().Remove(cmd.Path, cmd.Recursive, env.logger); err != nil {
		return err
	}
	return f.Save()
}

// StageStatusCmd is 'ocfl stage status'.
type StageStatusCmd struct {
	Offline bool `name:"offline" help:"skip checking the storage root for changes to the object since the stage was created"`
}

func (cmd *StageStatusCmd) Help() string {
	return `Shows the object and version the stage is for, whether it has changes to
commit, and any staged files that have changed since they were added, like
'git status'. Staged files are only read if their metadata has changed
since they were added (see 'ocfl stage --help'). If a staged file has
changed and its content is needed for the commit, status exits with an
error.

Status reads the storage root to check whether the object has changed since
the stage was created. It exits with an error if the stage is stale, or if
the check fails (for example, because the storage root can't be read). Use
--offline to skip the check.`
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
	up, upstreamErr := cmd.checkUpstream(ctx, env, s, ref)
	switch {
	case s.Frozen() && up == nil:
		fmt.Fprintf(env.stdout, "commit in progress or complete: %s (storage root not checked)\n", s.NextHead())
		fmt.Fprintf(env.stdout, "  to resume: %s\n", ref.command("commit", "--resume"))
		fmt.Fprintf(env.stdout, "  to revert: %s\n", ref.command("commit", "--revert"))
	case s.Frozen() && up.State == stage.UpstreamCommitted:
		// the staged files aren't needed to finish the commit
		fmt.Fprintf(env.stdout, "commit complete: %s is committed, but the stage file wasn't removed\n", s.NextHead())
		fmt.Fprintf(env.stdout, "  to remove it: %s\n", ref.command("commit", "--resume"))
		return upstreamErr
	case s.Frozen() && up.State == stage.UpstreamChanged:
		// the commit can't be resumed or reverted: upstreamErr says what to
		// do
	case s.Frozen():
		fmt.Fprintf(env.stdout, "commit in progress: %s\n", s.NextHead())
		fmt.Fprintf(env.stdout, "  to resume: %s\n", ref.command("commit", "--resume"))
		fmt.Fprintf(env.stdout, "  to revert: %s\n", ref.command("commit", "--revert"))
	case !stateDiff.Empty():
		fmt.Fprintln(env.stdout, "stage has changes to commit")
	default:
		fmt.Fprintln(env.stdout, "stage is unchanged and/or empty")
	}
	return errors.Join(upstreamErr, checkStagedFiles(ctx, env, f, up))
}

// checkUpstream checks the object in the storage root of the stage s, which
// ref names, for changes since s was created, and prints the upstream: line.
// It returns nil if the object wasn't checked, because of --offline or an
// error checking it. It returns an error if the check fails, or if s is
// stale.
func (cmd *StageStatusCmd) checkUpstream(ctx context.Context, env *cmdEnv, s *stage.Stage, ref stageRef) (*stage.Upstream, error) {
	const label = "upstream:     "
	if cmd.Offline {
		fmt.Fprintln(env.stdout, label+"not checked (--offline)")
		return nil, nil
	}
	root, err := env.openRoot(ctx, s.Root())
	var up stage.Upstream
	if err == nil {
		up, err = s.CheckUpstream(ctx, root)
	}
	if err != nil {
		fmt.Fprintln(env.stdout, label+"unknown (couldn't check the object)")
		return nil, &wrappedError{
			msg: "couldn't check the object in the storage root: " + err.Error() +
				"\n  to skip the check: " + ref.command("status", "--offline"),
			err: err,
		}
	}
	fmt.Fprintln(env.stdout, label+upstreamString(s, up))
	return &up, upstreamError(root, s, ref, up)
}

// StageStagesCmd is 'ocfl stage' with no sub-command: it lists the stages in
// the stage cache, in stage ID order, one per line, with the stage's short ID,
// object ID, and storage root location. -s, -f, --root and --check-stat are
// ignored. A stage file that can't be read is reported on stderr, and the
// command returns an error after listing the others.
//
// An object ID or storage root location with a control character (such as a
// tab or newline) is printed quoted, with %q, so it can't break the rows and
// columns. The output is for people: a quoted value can't be told apart from
// one that has quotes in it.
type StageStagesCmd struct{}

func (cmd *StageStagesCmd) Run(env *cmdEnv, parent *StageCmd) error {
	cache, err := parent.cache(env)
	if err != nil {
		return err
	}
	entries, err := cache.List()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(env.stdout, 0, 0, 2, ' ', 0)
	var bad int
	for _, e := range entries {
		path := cache.Path(e.ID)
		f, err := stage.Open(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// removed since it was listed: committed or deleted
			continue
		case err != nil:
			bad++
			env.logger.Error(fmt.Sprintf("can't read stage %s: %v%s", e.ShortID, err, unreadableStageHint(path)))
			continue
		}
		s := f.Stage()
		fmt.Fprintf(tw, "%s\t%s\t%s\n", e.ShortID, tableCell(s.ID()), tableCell(s.Root()))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if bad > 0 {
		return fmt.Errorf("%d of %d stage files can't be read", bad, len(entries))
	}
	return nil
}

// tableCell returns v for a cell in a table row: quoted if it has a control
// character, which could break the table's rows and columns.
func tableCell(v string) string {
	if strings.ContainsFunc(v, unicode.IsControl) {
		return strconv.Quote(v)
	}
	return v
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
	cacheFlags := slices.Clip(flags)
	flags = append(flags, "-s", short)
	return stageRef{path: cache.Path(id), flags: flags, cache: cache, cacheFlags: cacheFlags, id: id, shortID: short}, nil
}

// existingStageError returns the error for creating a stage when the stage
// file ref already exists.
func existingStageError(ref stageRef) error {
	f, err := stage.Open(ref.path)
	if err != nil {
		return fmt.Errorf("stage file already exists, but it can't be read: %w%s", err, unreadableStageHint(ref.path))
	}
	s := f.Stage()
	if s.Frozen() {
		return fmt.Errorf("a stage for %s already exists: %w", s.ID(), inProgressError(s, ref))
	}
	return fmt.Errorf("a stage for %s already exists: %s\n  to see it: %s\n  to delete it: %s",
		s.ID(), ref.path, ref.command("status"), ref.command("delete"))
}

// unreadableStageHint returns the text, starting with a newline, that says how
// to recover from the stage file at path that can't be read.
func unreadableStageHint(path string) string {
	return "\n  to start over, remove the stage file: " + path
}

// stageRef is a stage file and the flags that name it in 'ocfl stage'
// commands.
type stageRef struct {
	path  string
	flags []string

	// if the stage file is in the stage cache:
	cache      stage.Cache
	cacheFlags []string // the flags in flags that name cache
	id         string   // stage ID
	shortID    string   // the stage ID's shortest unique prefix, used in commands
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
		// the lock may not have been released
		errs := []error{existingStageError(r)}
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			for _, e := range joined.Unwrap() {
				if errors.Is(e, stage.ErrNotUnlocked) || errors.Is(e, stage.ErrLockBroken) {
					errs = append(errs, lockError(e, r))
				}
			}
		}
		return nil, errors.Join(errs...)
	}
	if err != nil {
		return nil, lockError(err, r)
	}
	return f, nil
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

// lockError adds to err what to do about the stage file lock error that it
// wraps, if any, for the stage file ref: [stage.ErrLocked] (the stage is
// locked by another command), [stage.ErrNotUnlocked] (this command couldn't
// release its lock), or [stage.ErrLockBroken] (this command's lock was
// removed while it held it). Otherwise, it returns err.
func lockError(err error, ref stageRef) error {
	var hint string
	switch {
	case errors.Is(err, stage.ErrLocked):
		hint = "if no other ocfl command is using the stage, clear the lock: " + ref.clearLockCommand()
	case errors.Is(err, stage.ErrNotUnlocked):
		hint = "to clear the lock: " + ref.clearLockCommand()
	case errors.Is(err, stage.ErrLockBroken):
		hint = "another command may have changed the stage at the same time: check it with: " + ref.command("status")
	default:
		return err
	}
	return fmt.Errorf("%w\n  %s", err, hint)
}

// clearLockCommand returns the command line that clears the lock on r's stage
// file. If the stage file is in the stage cache, the short stage ID in it is
// one that 'stage delete --lock' finds: unique among the stages with stage
// files or lock files.
func (r stageRef) clearLockCommand() string {
	if r.id == "" {
		return r.command("delete", "--lock")
	}
	short, err := r.cache.ShortLockID(r.id)
	if err != nil {
		short = r.id
	}
	lockRef := r
	lockRef.flags = append(slices.Clone(r.cacheFlags), "-s", short)
	return lockRef.command("delete", "--lock")
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
// is only in changed files. up is the state of the stage's object in the
// storage root, or nil if it wasn't checked: for a frozen stage, it sets what
// the error says to do.
func checkStagedFiles(ctx context.Context, env *cmdEnv, f *stage.File, up *stage.Upstream) error {
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
		err := fmt.Errorf("the stage can't be committed: %w", &stage.UnavailableError{Paths: result.Unavailable})
		var todo string
		switch {
		case !s.Frozen():
			todo = "add the files again or remove them from the stage"
		case up == nil:
			todo = "restore the files to resume the commit, or revert it; if the commit is already complete, --resume only removes the stage file"
		case up.State == stage.UpstreamAtBase:
			todo = "restore the files to resume the commit, or revert it"
		default:
			// the commit can't be resumed or reverted
			return err
		}
		return fmt.Errorf("%w\n  %s", err, todo)
	}
	return nil
}

// upstreamString describes up, the state of the object in the storage root of
// the stage s, for 'stage status'.
func upstreamString(s *stage.Stage, up stage.Upstream) string {
	next := s.NextHead()
	// only a stage for a new object has v1 as its next version
	newObject := next.Num() == 1
	switch {
	case up.State == stage.UpstreamAtBase && !s.Frozen():
		return "stage head matches upstream object"
	case up.State == stage.UpstreamAtBase:
		return fmt.Sprintf("commit of %s isn't complete", next)
	case up.State == stage.UpstreamCommitted:
		return fmt.Sprintf("%s (commit is complete)", next)
	case !up.Exists:
		return "no object (deleted since the stage was created)"
	case newObject && !up.Head.IsZero():
		return fmt.Sprintf("%s (created since the stage was created)", up.Head)
	case newObject:
		return "head unknown (object directory created since the stage was created)"
	case up.Head.IsZero():
		return "head unknown (changed since the stage was created)"
	case up.Head.Num() < next.Num():
		// the head isn't newer than the stage's base: the object was
		// replaced, such as by deleting and creating it again
		return fmt.Sprintf("%s (replaced since the stage was created)", up.Head)
	}
	return fmt.Sprintf("%s (changed since the stage was created)", up.Head)
}

// upstreamError returns an error wrapping [ocfl.ErrUpdateConflict] if up, the
// state of the object in root, shows that the stage s, which ref names, is
// stale. Otherwise, it returns nil.
func upstreamError(root *ocfl.Root, s *stage.Stage, ref stageRef, up stage.Upstream) error {
	if up.State != stage.UpstreamChanged {
		return nil
	}
	if s.Frozen() {
		return commitError(root, s, ref, ocfl.ErrUpdateConflict)
	}
	lines := []string{fmt.Sprintf("the stage for %s (%s) is stale: the object has changed since the stage was created", s.ID(), s.NextHead())}
	lines = append(lines, staleDraftGuidance(ref)...)
	if s.NextHead().Num() == 1 && up.Exists && up.Head.IsZero() {
		// the object directory has content, but no inventory that can be
		// read
		lines = append(lines, "  "+interruptedCommitHint)
	}
	return &wrappedError{msg: strings.Join(lines, "\n"), err: ocfl.ErrUpdateConflict}
}

// staleDraftGuidance returns what to do with the draft stage that ref names
// when the object has changed since the stage was created.
func staleDraftGuidance(ref stageRef) []string {
	return []string{"  the stage can't be committed: delete it and create a new one",
		"  to delete it: " + ref.command("delete")}
}

// closeStage closes the stage file f, which ref names, releasing its lock,
// and joins any error to *err, with what to do about it. It's deferred by
// commands that lock a stage file.
//
// done describes the command's work if it's finished when the stage file is
// gone, such as a commit, or it's empty. If the command succeeded, the stage
// file is gone, and its lock was removed by another command (see
// [stage.RemoveLock]), closeStage logs a warning instead of returning an
// error: the stage file was removed while the command held the lock, so
// there's nothing for another command to have changed.
func closeStage(env *cmdEnv, f *stage.File, ref stageRef, done string, err *error) {
	closeErr := f.Close()
	if done != "" && *err == nil && errors.Is(closeErr, stage.ErrLockBroken) {
		if _, statErr := os.Lstat(ref.path); errors.Is(statErr, fs.ErrNotExist) {
			env.logger.Warn(done+", but another command cleared the stage's lock while this one held it",
				"path", ref.path, "error", closeErr)
			return
		}
	}
	*err = errors.Join(*err, lockError(closeErr, ref))
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
		return nil, fmt.Errorf("%w\n%s", err, interruptedCommitHint)
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
