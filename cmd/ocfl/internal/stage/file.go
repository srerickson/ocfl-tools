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
	"os"
	"path/filepath"

	"github.com/srerickson/ocfl-go"
)

var (
	// ErrFrozen is returned when a frozen stage is committed again or deleted:
	// its commit is in progress, and must be resumed or reverted first.
	ErrFrozen = errors.New("the stage has a commit in progress")

	// ErrNotFrozen is returned when the commit of a draft stage is resumed or
	// reverted.
	ErrNotFrozen = errors.New("the stage has no commit in progress")

	// ErrNotRemoved is returned when a commit is complete, but its stage file
	// couldn't be removed. Resuming the commit removes it.
	ErrNotRemoved = errors.New("the commit is complete, but the stage file couldn't be removed")
)

// DraftNotSavedError is returned by [File.Commit] when the object has changed
// since the stage was created, so the commit failed without changing the
// object, but the stage couldn't be saved as a draft again. The stage is still
// frozen, as it is in the stage file. It wraps both errors, so it wraps
// [ocfl.ErrUpdateConflict].
type DraftNotSavedError struct {
	Err     error // the conflict, which wraps ocfl.ErrUpdateConflict
	SaveErr error // the error saving the draft
}

func (e *DraftNotSavedError) Error() string {
	return fmt.Sprintf("%v; the stage couldn't be saved as a draft: %v", e.Err, e.SaveErr)
}

func (e *DraftNotSavedError) Unwrap() []error { return []error{e.Err, e.SaveErr} }

// File is a stage saved in a stage file. Commit, Resume, Revert, Delete and
// Discard keep the file in sync with the stage: when they return, the file
// has the stage's current state, or has been removed. The one exception is
// a revert whose draft can't be saved: the stage is a draft, but the file
// still has the frozen stage (see [File.Revert]). After changing a draft
// stage with [Stage.AddDir], [Stage.AddFile] or [Stage.Remove], save it with
// [File.Save].
//
// A commit has two phases. [File.Commit] freezes the stage with the new
// version's metadata and saves it before it changes the object, so that if
// the commit is interrupted or fails, the frozen stage in the file is all
// that's needed to finish it with [File.Resume] or undo it with
// [File.Revert]. When the commit is complete, the file is removed. If it
// can't be removed, Commit and Resume return an error wrapping
// [ErrNotRemoved], and the file still has the frozen stage, so resuming the
// commit removes it.
type File struct {
	path  string
	stage *Stage
}

// Open reads the stage file name.
func Open(name string) (*File, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var s Stage
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("reading stage file %s: %w", name, err)
	}
	return &File{path: name, stage: &s}, nil
}

// Create saves s in the new stage file name. If the file exists, it isn't
// replaced: Create returns an error that wraps [fs.ErrExist].
func Create(name string, s *Stage) (*File, error) {
	if s == nil {
		panic("stage.Create: nil stage")
	}
	err := write(name, s, func(tmp string) error {
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
	if err != nil {
		return nil, err
	}
	return &File{path: name, stage: s}, nil
}

// Path returns the path of the stage file.
func (f *File) Path() string { return f.path }

// Stage returns the stage saved in the file.
func (f *File) Stage() *Stage { return f.stage }

// Save saves the stage in the file. The file is replaced atomically, so an
// interrupted Save leaves the previous stage file in place.
func (f *File) Save() error {
	return write(f.path, f.stage, func(tmp string) error { return os.Rename(tmp, f.path) })
}

// Check is like [Stage.Check], but it also saves the refreshed records of
// unchanged files, so the next check doesn't read them. Like 'git status',
// saving them is opportunistic: if it fails, a warning is logged to logger,
// which may be nil.
func (f *File) Check(ctx context.Context, logger *slog.Logger) (*CheckResult, error) {
	result, err := f.stage.Check(ctx)
	if err != nil {
		return nil, err
	}
	if result.Refreshed {
		if err := f.Save(); err != nil {
			loggerOrDiscard(logger).Warn("couldn't save refreshed file stats in the stage file", "err", err)
		}
	}
	return result, nil
}

// Delete removes the stage file of a draft stage. If the stage is frozen,
// it returns [ErrFrozen]: the commit must be resumed or reverted first, or the
// stage discarded with [File.Discard].
func (f *File) Delete() error {
	if f.stage.Frozen() {
		return ErrFrozen
	}
	return f.remove()
}

// Discard removes the stage file, even if the stage is frozen. Discarding a
// frozen stage leaves its commit unfinished: the object may be partially
// updated, and the commit can't be resumed or reverted.
func (f *File) Discard() error { return f.remove() }

// Commit commits the draft stage to the object in root, as a new version with
// the message msg and the user, then removes the stage file. The staged files
// are checked before the stage is frozen, so if they aren't available, Commit
// returns an error wrapping [ocfl.ErrMissingContent] and the stage is still a
// draft. Then the frozen stage is saved, and the object is updated. If the
// frozen stage can't be saved, the stage is still a draft, and so is the stage
// in the file.
//
// If the object has changed since the stage was created, Commit returns an
// error wrapping [ocfl.ErrUpdateConflict] without changing the object. The
// stage can never be committed, and it's saved as a draft again, so it can be
// deleted. If it can't be saved, the error is a *[DraftNotSavedError], and the
// stage is still frozen.
//
// Otherwise, if Commit returns an error, the stage is frozen, and the object
// may be partially updated: the commit can be resumed or reverted. If the stage
// was frozen when Commit was called, it returns [ErrFrozen]. Progress is logged
// to logger, which may be nil.
func (f *File) Commit(ctx context.Context, root *ocfl.Root, msg string, user ocfl.User, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Commit: nil root")
	}
	s := f.stage
	if s.Frozen() {
		return ErrFrozen
	}
	content, closeContent, err := s.localContent(ctx)
	if err != nil {
		return err
	}
	defer closeContent()
	if err := s.freeze(msg, user); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		// the file has the draft. unfreeze can't fail: s was frozen by freeze.
		_ = s.unfreeze()
		return fmt.Errorf("saving the stage file before commit: %w", err)
	}
	err = f.apply(ctx, root, content, logger)
	if errors.Is(err, ocfl.ErrUpdateConflict) {
		// nothing was written by the first attempt to apply s
		if saveErr := f.saveUnfrozen(); saveErr != nil {
			return &DraftNotSavedError{Err: err, SaveErr: saveErr}
		}
	}
	return err
}

// Resume finishes the interrupted commit of the frozen stage to the object
// in root, using the saved version metadata, then removes the stage file. If
// the stage is a draft, it returns [ErrNotFrozen]. If Resume returns an
// error, the stage is still frozen: the object may be partially updated. If
// the error wraps [ocfl.ErrUpdateConflict], the object was changed by
// something else after the commit was interrupted, and the commit can't be
// resumed or reverted. Progress is logged to logger, which may be nil.
func (f *File) Resume(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Resume: nil root")
	}
	if !f.stage.Frozen() {
		return ErrNotFrozen
	}
	content, closeContent, err := f.stage.localContent(ctx)
	if err != nil {
		return err
	}
	defer closeContent()
	return f.apply(ctx, root, content, logger)
}

// Revert undoes the interrupted commit of the frozen stage to the object in
// root, and saves the stage as a draft, so it can be changed and committed
// again, or deleted. If the stage is a draft, it returns [ErrNotFrozen]. If
// the commit is complete, it returns an error wrapping
// [ocfl.ErrUpdateCompleted], and if the object was changed by something else,
// an error wrapping [ocfl.ErrUpdateConflict]. If the commit is reverted, but
// the draft can't be saved, Revert returns an error and the stage is a draft,
// but the stage file still has the frozen stage: its commit must not be
// resumed. Reverting it again (see [Open]) finishes reverting. Progress is
// logged to logger, which may be nil.
func (f *File) Revert(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Revert: nil root")
	}
	s := f.stage
	if !s.Frozen() {
		return ErrNotFrozen
	}
	logger = loggerOrDiscard(logger)
	logger.Info("reverting object update", "object_id", s.ID(), "object_version", s.NextHead())
	if err := root.Revert(ctx, s.update, ocfl.ApplyWithLogger(logger)); err != nil {
		return err
	}
	// Revert made the update a draft
	s.draft = nil
	if err := f.Save(); err != nil {
		return fmt.Errorf("the commit was reverted, but the stage file couldn't be saved: %w", err)
	}
	return nil
}

// apply applies the frozen stage to the object in root, with content, and
// removes the stage file, if it's still there.
func (f *File) apply(ctx context.Context, root *ocfl.Root, content ocfl.ContentSource, logger *slog.Logger) error {
	s := f.stage
	logger = loggerOrDiscard(logger)
	logger.Info("starting object update", "object_id", s.ID(), "object_version", s.NextHead())
	if _, err := root.Apply(ctx, s.update, content, ocfl.ApplyWithLogger(logger)); err != nil {
		return err
	}
	logger.Info("object update complete", "object_id", s.ID(), "object_version", s.NextHead())
	if err := f.remove(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %w", ErrNotRemoved, err)
	}
	return nil
}

// saveUnfrozen unfreezes the stage and saves it. If it can't be saved, the
// stage is frozen again, so it matches the stage file.
func (f *File) saveUnfrozen() error {
	frozen, draft := f.stage.update, f.stage.draft
	if err := f.stage.unfreeze(); err != nil {
		return err
	}
	if err := f.Save(); err != nil {
		f.stage.update, f.stage.draft = frozen, draft
		return err
	}
	return nil
}

// remove removes the stage file and syncs its directory.
func (f *File) remove() error {
	if err := os.Remove(f.path); err != nil {
		return err
	}
	syncDir(filepath.Dir(f.path))
	return nil
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

// loggerOrDiscard returns logger, or if it's nil, a logger that discards
// everything.
func loggerOrDiscard(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return logger
}
