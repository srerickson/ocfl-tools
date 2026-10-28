package run

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/srerickson/ocfl-go"
	"github.com/srerickson/ocfl-go/digest"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

// CommitCmd is 'ocfl commit': it commits a directory as a new object version.
type CommitCmd struct {
	ID       string `name:"id" short:"i" help:"The ID for the object to create or update"`
	Message  string `name:"message" short:"m" help:"Message to include in the object version metadata"`
	Name     string `name:"name" short:"n" ocflenv:"OCFL_USER_NAME" help:"Username to include in the object version metadata"`
	Email    string `name:"email" short:"e" ocflenv:"OCFL_USER_EMAIL" help:"User email to include in the object version metadata"`
	Alg      string `name:"alg" default:"sha512" help:"Digest algorithm (ignored for commits to existing objects)"`
	NoHidden bool   `name:"no-hidden" help:"exclude hidden files and directories (.*)"`
	Path     string `arg:"" name:"path" help:"local directory with object state to commit"`
}

func (cmd *CommitCmd) Help() string {
	return `The new version's state is the contents of the directory. The commit is
saved in a temporary stage file before the object is changed. If the commit
is interrupted or fails, the stage file is kept, and the error message gives
the 'ocfl stage commit' commands that resume or revert the commit.`
}

func (cmd *CommitCmd) Run(ctx context.Context, env *cmdEnv) error {
	alg, err := digest.DefaultRegistry().Get(cmd.Alg)
	if err != nil {
		return err
	}
	absPath, err := filepath.Abs(cmd.Path)
	if err != nil {
		return err
	}
	root, rootLoc, err := env.getRootLocation(ctx)
	if err != nil {
		return err
	}
	if err := checkWritable(root); err != nil {
		return err
	}
	s, err := newStage(ctx, root, rootLoc, cmd.ID, alg)
	if err != nil {
		return err
	}
	opts := []stage.AddOption{stage.AddAndRemove()}
	if cmd.NoHidden {
		opts = append(opts, stage.AddWithoutHidden())
	}
	if err := s.AddDir(ctx, absPath, env.logger, opts...); err != nil {
		return err
	}
	ref, err := fileRef(tempStageFile())
	if err != nil {
		return err
	}
	f, err := ref.create(s)
	if err != nil {
		return fmt.Errorf("saving the stage file before commit: %w", err)
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
	}
	// Nothing was written to the object, and the stage was only for this
	// commit: it's removed, so the command can be run again.
	conflict := errors.Is(err, ocfl.ErrUpdateConflict)
	if rmErr := f.Delete(); rmErr != nil {
		notRemoved := "  the stage file couldn't be removed: " + rmErr.Error()
		deleteIt := "  to delete it: " + ref.command("delete")
		if conflict {
			return conflictError(s, err, notRemoved, deleteIt)
		}
		return fmt.Errorf("%w\n%s\n%s", err, notRemoved, deleteIt)
	}
	if conflict {
		return conflictError(s, err, "  to commit to the object's current version, run the command again")
	}
	return err
}

// tempStageFile returns the path of a new stage file in the temporary
// directory.
func tempStageFile() string {
	return filepath.Join(os.TempDir(), "ocfl-commit-"+rand.Text()+".json")
}
