package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
seconds, and mode.`
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
	s, err := stage.New(ctx, root, cmd.ID, alg)
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
	s, err := parent.readFile()
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
}

func (cmd *StageCommitCmd) Run(ctx context.Context, env *cmdEnv, parent *StageCmd) error {
	s, err := parent.readFile()
	if err != nil {
		return err
	}
	root, err := env.getRoot(ctx)
	if err != nil {
		return err
	}
	updated, err := objectUpdateOrRevert(ctx, env, root, s, cmd.Message, newUser(cmd.Name, cmd.Email))
	if err != nil {
		return err
	}
	if updated {
		env.logger.Info("removing stage file", "path", parent.File)
		if err := os.Remove(parent.File); err != nil {
			return fmt.Errorf("removing stage file: %w", err)
		}
	}
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
	s, err := parent.readFile()
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
		fmt.Fprintln(env.stdout, `  (use "ocfl stage add" to stage the new content)`)
		for _, c := range result.Changed {
			if c.Err != nil {
				fmt.Fprintf(env.stdout, "  error:    %s: %v\n", c.Path, c.Err)
				continue
			}
			fmt.Fprintf(env.stdout, "  %-9s %s (%s)\n", c.Status.String()+":", c.Path, c.Source)
		}
	}
	if len(result.Unavailable) > 0 {
		return fmt.Errorf("the stage can't be committed: %w\n  add the files again or remove them from the stage", &stage.UnavailableError{Paths: result.Unavailable})
	}
	return nil
}

func newUser(name string, email string) ocfl.User {
	if email != "" && !strings.HasPrefix(email, "email:") {
		email = "email:" + email
	}
	return ocfl.User{Name: name, Address: email}
}

// stagedContent returns the content source for committing s: its unchanged
// staged files.
func stagedContent(ctx context.Context, env *cmdEnv, s *stage.Stage) (ocfl.ContentSource, error) {
	return s.Content(ctx, func(dir string) (ocflfs.FS, error) {
		return env.localFS(dir)
	})
}

// objectUpdateOrRevert finalizes the update in s and applies it with content
// from staged files, reverting partial updates if ctx is canceled. The
// returned bool indicates if the update completed without being interrupted.
func objectUpdateOrRevert(ctx context.Context, env *cmdEnv, root *ocfl.Root, s *stage.Stage, msg string, user ocfl.User) (bool, error) {
	// staged files are checked before the stage is finalized
	content, err := stagedContent(ctx, env, s)
	if err != nil {
		return false, fmt.Errorf("the stage can't be committed: %w\n  add the files again or remove them from the stage", err)
	}
	if err := s.Finalize(msg, user); err != nil {
		return false, err
	}
	env.logger.Info("starting object update", "object_id", s.ID(), "object_version", s.NextHead())
	if _, err := root.Apply(ctx, s.Update(), content, ocfl.ApplyWithLogger(env.logger)); err != nil {
		if errors.Is(err, context.Canceled) {
			env.logger.Info("object update interrupted: reverting to last valid state")
			// ctx is already canceled: revert regardless.
			if err := root.Revert(context.WithoutCancel(ctx), s.Update(), ocfl.ApplyWithLogger(env.logger)); err != nil {
				return false, fmt.Errorf("while reverting object update: %w", err)
			}
			env.logger.Info("object update was interrupted and successfully reverted")
			return false, nil
		}
		return false, fmt.Errorf("during object update: %w", err)
	}
	env.logger.Info("object update complete", "object_id", s.ID(), "object_version", s.NextHead())
	return true, nil
}
