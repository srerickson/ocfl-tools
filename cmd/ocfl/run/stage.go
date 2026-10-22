package run

import (
	"context"
	"errors"
	"fmt"
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

type StageCmd struct {
	File      string `name:"file" short:"f" default:"ocfl-stage.json" help:"path to stage file"`
	CheckStat string `name:"check-stat" enum:"default,minimal" default:"default" ocflenv:"OCFL_CHECK_STAT" help:"file metadata compared to find staged files that haven't changed: 'default' or 'minimal' (size, mtime in seconds, and mode; for network file systems)"`

	Add    StageAddCmd    `cmd:"" help:"Add a file or directory to the stage"`
	Commit StageCommitCmd `cmd:"" help:"Commit the stage as a new object version"`
	Diff   StageDiffCmd   `cmd:"" help:"Show changes between an upstream object or directory and the stage"`
	Ls     StageListCmd   `cmd:"" help:"List files in the stage state"`
	New    NewStageCmd    `cmd:"" help:"Create a new stage for preparing updates to an object"`
	Rm     StageRmCmd     `cmd:"" help:"Remove a file or directory from the stage"`
	Status StageStatusCmd `cmd:"" help:"Show stage details and report any errors"`
}

func (cmd *StageCmd) Help() string {
	return `A stage file records changes to an object that can be committed as a new
version. Create one with 'stage new', change it with 'stage add' and 'stage
rm', and commit it with 'stage commit', which removes the stage file. Staged
content is digested when it's added, but it isn't copied until the stage is
committed.

    ocfl stage new --id my-object
    ocfl stage add my-files/
    ocfl stage rm old-file.txt
    ocfl stage commit -m "update data" -n "My Name" -e me@example.net

The stage file records each staged file's metadata (size, modification
time, inode, and so on), like git's index. 'stage status' and 'stage add'
only read staged files whose metadata has changed. On network file systems
(SMB/CIFS, NFS), where the inode and change time aren't stable, use
--check-stat=minimal to compare only the size, modification time in whole
seconds, and mode.

If a commit is interrupted, the stage file is kept. Run 'stage commit
--resume' to finish the commit, or 'stage commit --revert' to undo the
partial update and return the stage to its uncommitted state.`
}

// readFile reads the stage file named by the --file flag.
func (cmd *StageCmd) readFile() (*stage.Stage, error) {
	s, err := stage.Read(cmd.File)
	if err != nil {
		return nil, err
	}
	if cmd.CheckStat == "minimal" {
		s.Compare = statcache.CompareMinimal
	}
	return s, nil
}

// readDraft reads the stage file named by the --file flag. It returns an error
// if the stage has been finalized for a commit, which must be resumed or
// reverted before the stage can be changed.
func (cmd *StageCmd) readDraft(env *cmdEnv) (*stage.Stage, error) {
	s, err := cmd.readFile()
	if err != nil {
		return nil, err
	}
	if s.Finalized() {
		return nil, fmt.Errorf("the stage can't be changed: a commit of %s (%s) is in progress\n  to resume: %s\n  to revert: %s",
			s.ID(), s.NextHead(),
			env.stageCommitCommand(cmd.File, false),
			env.stageCommitCommand(cmd.File, true))
	}
	return s, nil
}

// stage new
type NewStageCmd struct {
	Alg string `name:"alg" default:"sha512" help:"Digest Algorithm used to digest content. Ignored for existing objects."`
	ID  string `name:"id" short:"i" required:"" help:"object id for the new stage"`
}

func (cmd *NewStageCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	if _, err := os.Stat(parent.File); err == nil {
		return fmt.Errorf("stage file already exists: %s", parent.File)
	}
	alg, err := digest.DefaultRegistry().Get(cmd.Alg)
	if err != nil {
		return err
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	s, err := newStage(ctx, root, cmd.ID, alg)
	if err != nil {
		return err
	}
	if err := stage.Write(parent.File, s); err != nil {
		return err
	}
	env.logger.Info("stage file created", "path", parent.File, "object_id", s.ID(), "object_version", s.NextHead())
	return nil
}

// stage add
type StageAddCmd struct {
	NoHidden bool   `name:"no-hidden" help:"exclude hidden files and directories (.*). Ignored if path is a file."`
	As       string `name:"as" help:"logical name for the new content. Default: base name if path is a file; '.' if path is a directory."`
	Jobs     int    `name:"jobs" short:"j" default:"0" help:"number of files to digest concurrently. Defaults to the number of CPU cores."`
	Remove   bool   `name:"remove" help:"also remove staged files not found in the path. Ignored if path is a file."`
	Path     string `arg:"" help:"file or parent directory for content to add to the stage"`
}

func (cmd *StageAddCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readDraft(env)
	if err != nil {
		return err
	}
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
	return stage.Write(parent.File, s)
}

// stage commit
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
--revert. After a revert, the stage can be changed and committed again.`
}

func (cmd *StageCommitCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd, kctx *kong.Context) error {
	s, err := parent.readFile()
	if err != nil {
		return err
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	switch {
	case cmd.Revert:
		return cmd.revert(ctx, env, root, s, parent.File, kctx)
	case cmd.Resume:
		return cmd.resume(ctx, env, root, s, parent.File, kctx)
	case s.Finalized():
		return fmt.Errorf("a commit of %s (%s) is in progress\n  to resume: %s\n  to revert: %s",
			s.ID(), s.NextHead(),
			env.stageCommitCommand(parent.File, false),
			env.stageCommitCommand(parent.File, true))
	}
	user := newUser(cmd.Name, cmd.Email)
	// staged files are checked before the stage is finalized, so it can
	// still be changed if they aren't available.
	content, err := stagedContent(ctx, env, s)
	if err != nil {
		return fmt.Errorf("the stage can't be committed: %w\n  add the files again or remove them from the stage", err)
	}
	if err := s.Finalize(cmd.Message, user); err != nil {
		return err
	}
	// The finalized stage is saved before the object is changed: it's all
	// that's needed to resume or revert the commit.
	if err := stage.Write(parent.File, s); err != nil {
		return fmt.Errorf("saving the stage file before commit: %w", err)
	}
	return applyStage(ctx, env, root, s, parent.File, false, content)
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

// resume finishes the interrupted commit of the finalized stage s, which is
// saved in the stage file stagePath, using the saved version metadata.
func (cmd *StageCommitCmd) resume(ctx context.Context, env *cmdEnv, root *ocfl.Root, s *stage.Stage, stagePath string, kctx *kong.Context) error {
	if err := checkNoVersionFlags(kctx, "--resume"); err != nil {
		return err
	}
	if !s.Finalized() {
		return errors.New("stage isn't finalized: nothing to resume")
	}
	content, err := stagedContent(ctx, env, s)
	if err != nil {
		return env.commitError(root, s, stagePath, true, err)
	}
	return applyStage(ctx, env, root, s, stagePath, true, content)
}

// revert reverts the interrupted commit of the finalized stage s, which is
// saved in the stage file stagePath, and saves the stage as a draft.
func (cmd *StageCommitCmd) revert(ctx context.Context, env *cmdEnv, root *ocfl.Root, s *stage.Stage, stagePath string, kctx *kong.Context) error {
	if err := checkNoVersionFlags(kctx, "--revert"); err != nil {
		return err
	}
	if !s.Finalized() {
		return errors.New("stage isn't finalized: nothing to revert")
	}
	env.logger.Info("reverting object update", "object_id", s.ID(), "object_version", s.NextHead())
	err := root.Revert(ctx, s.Update(), ocfl.ApplyWithLogger(env.logger))
	switch {
	case errors.Is(err, ocfl.ErrUpdateCompleted):
		return fmt.Errorf("the commit of %s (%s) is complete and can't be reverted; to finish the commit and remove the stage file, run:\n  %s",
			s.ID(), s.NextHead(), env.stageCommitCommand(stagePath, false))
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("revert of %s (%s) was interrupted\n  to finish reverting: %s",
			s.ID(), s.NextHead(), env.stageCommitCommand(stagePath, true))
	case err != nil:
		return fmt.Errorf("reverting the commit of %s (%s): %w", s.ID(), s.NextHead(), err)
	}
	// s is a draft again
	if err := stage.Write(stagePath, s); err != nil {
		return fmt.Errorf("the commit was reverted, but the stage file couldn't be saved: %w", err)
	}
	env.logger.Info("object update reverted: the stage can be changed and committed again", "path", stagePath)
	return nil
}

// stage diff
type StageDiffCmd struct {
	Dir string `name:"dir" help:"use a local directory rather than upstream object as basis for comparison to the stage."`
}

func (cmd *StageDiffCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readFile()
	if err != nil {
		return err
	}
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

// 'stage list' command
type StageListCmd struct {
	WithDigests bool `name:"digests" short:"d" help:"include file digests in output"`
}

func (cmd *StageListCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readFile()
	if err != nil {
		return err
	}
	s.List(env.stdout, cmd.WithDigests)
	return nil
}

// 'stage rm' command
type StageRmCmd struct {
	Recursive bool   `name:"recursive" short:"r" help:"remove all files in the directory"`
	Path      string `arg:"" name:"path" help:"file or directory to remove"`
}

func (cmd *StageRmCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readDraft(env)
	if err != nil {
		return err
	}
	if err := s.Remove(cmd.Path, cmd.Recursive, env.logger); err != nil {
		return err
	}
	return stage.Write(parent.File, s)
}

// 'stage status' command
type StageStatusCmd struct{}

func (cmd *StageStatusCmd) Help() string {
	return `Shows the object and version the stage is for, whether it has changes to
commit, and any staged files that have changed since they were added, like
'git status'. Staged files are only read if their metadata has changed
since they were added (see 'ocfl stage --help'). If a staged file has
changed and its content is needed for the commit, status exits with an
error.`
}

func (cmd *StageStatusCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readFile()
	if err != nil {
		return err
	}
	upd := s.Update()
	fmt.Fprintf(env.stdout, "object:      %s (%s)\n", s.ID(), s.NextHead())
	fmt.Fprintf(env.stdout, "digest alg:  %s\n", s.DigestAlgorithm().ID())
	fmt.Fprintf(env.stdout, "state size:  %d files\n", len(upd.NewState().PathMap()))
	stateDiff, err := diff.Diff(upd.BaseState().PathMap(), upd.NewState().PathMap())
	if err != nil {
		return err
	}
	switch {
	case s.Finalized():
		fmt.Fprintf(env.stdout, "commit in progress: %s\n", s.NextHead())
		fmt.Fprintf(env.stdout, "  to resume: %s\n", env.stageCommitCommand(parent.File, false))
		fmt.Fprintf(env.stdout, "  to revert: %s\n", env.stageCommitCommand(parent.File, true))
	case !stateDiff.Empty():
		fmt.Fprintln(env.stdout, "stage has changes to commit")
	default:
		fmt.Fprintln(env.stdout, "stage is unchanged and/or empty")
	}
	return checkStagedFiles(ctx, env, s, parent.File)
}

// checkStagedFiles reports staged files in s that have changed since they were
// added, and saves the refreshed stats of unchanged files in the stage file
// stagePath. It returns an error if the commit needs content that is only in
// changed files.
func checkStagedFiles(ctx context.Context, env *cmdEnv, s *stage.Stage, stagePath string) error {
	result, err := s.Check(ctx)
	if err != nil {
		return err
	}
	if result.Refreshed {
		// like 'git status', saving the refreshed stats is opportunistic
		if err := stage.Write(stagePath, s); err != nil {
			env.logger.Warn("couldn't save refreshed file stats in the stage file", "err", err)
		}
	}
	if len(result.Changed) > 0 {
		fmt.Fprintln(env.stdout, "staged files changed since they were added:")
		if !s.Finalized() {
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
		if s.Finalized() {
			todo = "restore the files to resume the commit, or revert it"
		}
		return fmt.Errorf("the stage can't be committed: %w\n  %s", &stage.UnavailableError{Paths: result.Unavailable}, todo)
	}
	return nil
}

func newUser(name string, email string) ocfl.User {
	if email != "" && !strings.HasPrefix(email, "email:") {
		email = "email:" + email
	}
	return ocfl.User{Name: name, Address: email}
}

// newStage starts a stage to the object id in root. alg is used if the
// object doesn't exist.
func newStage(ctx context.Context, root *ocfl.Root, id string, alg digest.Algorithm) (*stage.Stage, error) {
	s, err := stage.New(ctx, root, id, alg)
	if errors.Is(err, ocfl.ErrObjectIncomplete) {
		return nil, fmt.Errorf("%w\nif a commit to the object was interrupted, resume or revert it using its stage file", err)
	}
	return s, err
}

// stagedContent returns the content source for committing s: its unchanged
// staged files.
func stagedContent(ctx context.Context, env *cmdEnv, s *stage.Stage) (ocfl.ContentSource, error) {
	return s.Content(ctx, func(dir string) (ocflfs.FS, error) {
		fsys, err := env.localFS(dir)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", errStagedContent, err)
		}
		return fsys, nil
	})
}

// applyStage applies the finalized update in s, which is saved in the stage
// file stagePath, with content from staged files, and removes the stage
// file. resuming is true if an earlier attempt to apply s may have changed the
// object. If the update isn't applied, the stage file is kept and the error
// says how to resume or revert the commit.
func applyStage(ctx context.Context, env *cmdEnv, root *ocfl.Root, s *stage.Stage, stagePath string, resuming bool, content ocfl.ContentSource) error {
	env.logger.Info("starting object update", "object_id", s.ID(), "object_version", s.NextHead())
	if _, err := root.Apply(ctx, s.Update(), content, ocfl.ApplyWithLogger(env.logger)); err != nil {
		return env.commitError(root, s, stagePath, resuming, err)
	}
	env.logger.Info("object update complete", "object_id", s.ID(), "object_version", s.NextHead())
	if err := os.Remove(stagePath); err != nil {
		return fmt.Errorf("removing stage file: %w", err)
	}
	return nil
}

// errStagedContent wraps errors opening the directories staged files were
// added from.
var errStagedContent = errors.New("staged content isn't available")

// commitError describes the error err from applying the stage s, saved in the
// stage file stagePath, and says what to do next. resuming is true if an
// earlier attempt to apply s may have changed the object.
func (env *cmdEnv) commitError(root *ocfl.Root, s *stage.Stage, stagePath string, resuming bool, err error) error {
	commit := fmt.Sprintf("commit of %s (%s)", s.ID(), s.NextHead())
	objPath := "  object path: " + objectLocation(root, s.ID())
	resume := env.stageCommitCommand(stagePath, false)
	revert := "  to revert: " + env.stageCommitCommand(stagePath, true)
	var lines []string
	switch {
	case errors.Is(err, context.Canceled):
		lines = []string{commit + " was interrupted; the object may be partially updated",
			objPath, "  to resume: " + resume, revert}
	case errors.Is(err, ocfl.ErrUpdateConflict):
		lines = []string{commit + " failed: " + err.Error(),
			objPath, "  the stage file was kept: " + stagePath}
	case errors.Is(err, ocfl.ErrContentChanged),
		resuming && (errors.Is(err, ocfl.ErrMissingContent) || errors.Is(err, errStagedContent)):
		// Apply finds changed content as it copies it, after other content
		// may have been written, and a resumed commit may have been
		// interrupted after changing the object.
		lines = []string{commit + " failed; the object may be partially updated: " + err.Error(),
			objPath, "  to resume after restoring the staged files: " + resume, revert}
	case errors.Is(err, ocfl.ErrMissingContent), errors.Is(err, errStagedContent):
		// Apply checks for missing content before it writes anything.
		lines = []string{commit + " failed before the object was changed: " + err.Error(),
			"  to resume after restoring the staged files: " + resume, revert}
	default:
		lines = []string{commit + " failed; the object may be partially updated: " + err.Error(),
			objPath, "  to resume: " + resume, revert}
	}
	return &wrappedError{msg: strings.Join(lines, "\n"), err: err}
}

// stageCommitCommand returns the 'stage commit' command line that resumes, or
// if revert is true reverts, a commit using the stage file stagePath.
func (env *cmdEnv) stageCommitCommand(stagePath string, revert bool) string {
	args := []string{"ocfl"}
	if env.rootLocation != "" {
		args = append(args, "--root", env.rootLocation)
	}
	args = append(args, "stage", "commit", "-f", stagePath)
	if revert {
		args = append(args, "--revert")
	} else {
		args = append(args, "--resume")
	}
	for i, arg := range args {
		args[i] = shellQuote(arg)
	}
	return strings.Join(args, " ")
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

// flagGiven returns true if the flag name was given on the command line, as
// opposed to set from the environment or a default.
func flagGiven(kctx *kong.Context, name string) bool {
	for _, p := range kctx.Path {
		if p.Flag != nil && !p.Resolved && p.Flag.Name == name {
			return true
		}
	}
	return false
}

// shellQuote quotes s for a POSIX shell if it has characters that need it.
func shellQuote(s string) string {
	safe := func(r rune) bool {
		return r < 128 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:=@%+,", r))
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !safe(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// wrappedError is an error with a message that replaces err's.
type wrappedError struct {
	msg string
	err error
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.err }
