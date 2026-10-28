package stage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

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

	// ErrLocked is returned when a stage file can't be locked because it's
	// locked by another command: its lock file exists. The lock file is the
	// stage file's name with ".lock" appended.
	ErrLocked = errors.New("the stage file is locked by another command")
)

// errNotLocked is returned by the methods that change the stage file when the
// File doesn't hold its lock.
var errNotLocked = errors.New("the stage file isn't locked: it can't be changed")

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
// Only a File that holds the stage file's lock can change the file: one
// returned by [OpenLocked] or [Create], until it's closed with [File.Close].
// The lock is a lock file, the stage file's name with ".lock" appended, so
// that two commands can't change the same stage file, and the stage in a
// locked File is always the one in the stage file. A File returned by [Open]
// can be read and checked, but its methods that change the file return an
// error.
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
	saved []byte // the stage file's contents when it was last read or written
	lock  string // the lock file, if f holds the lock
}

// Open reads the stage file name without locking it. The File can be read and
// checked, but it can't change the stage file: use [OpenLocked] for that.
func Open(name string) (*File, error) {
	return read(name)
}

// OpenLocked locks the stage file name, then reads it. The lock is held until
// [File.Close] is called. If the stage file is locked by another command,
// OpenLocked waits for a moment (Check may lock it briefly), then returns an
// error wrapping [ErrLocked], which names the lock file. If OpenLocked returns
// an error, the lock isn't held.
func OpenLocked(name string) (*File, error) {
	lock, err := lockFile(name)
	if err != nil {
		return nil, err
	}
	f, err := read(name)
	if err != nil {
		return nil, errors.Join(err, unlockFile(lock))
	}
	f.lock = lock
	return f, nil
}

// Create locks the new stage file name and saves s in it. The lock is held
// until [File.Close] is called. If the file exists, it isn't replaced: Create
// returns an error that wraps [fs.ErrExist]. If it's locked by another
// command, Create returns an error wrapping [ErrLocked]. If Create returns an
// error, the lock isn't held.
func Create(name string, s *Stage) (*File, error) {
	if s == nil {
		panic("stage.Create: nil stage")
	}
	lock, err := lockFile(name)
	if err != nil {
		return nil, err
	}
	b, err := write(name, s, func(tmp string) error {
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
		return nil, errors.Join(err, unlockFile(lock))
	}
	return &File{path: name, stage: s, saved: b, lock: lock}, nil
}

// Path returns the path of the stage file.
func (f *File) Path() string { return f.path }

// Stage returns the stage saved in the file.
func (f *File) Stage() *Stage { return f.stage }

// Close releases the stage file's lock, if f holds it. After Close, f can't
// change the stage file. Calling Close again does nothing.
func (f *File) Close() error {
	if f.lock == "" {
		return nil
	}
	lock := f.lock
	f.lock = ""
	return unlockFile(lock)
}

// Save saves the stage in the file. The file is replaced atomically, so an
// interrupted Save leaves the previous stage file in place.
func (f *File) Save() error {
	if f.lock == "" {
		return errNotLocked
	}
	b, err := write(f.path, f.stage, func(tmp string) error { return os.Rename(tmp, f.path) })
	if err != nil {
		return err
	}
	f.saved = b
	return nil
}

// Check is like [Stage.Check], but it also saves the refreshed records of
// unchanged files, so the next check doesn't read them. Like 'git status',
// saving them is opportunistic, and Check doesn't need the stage file's lock.
// If f doesn't hold the lock, Check takes it to save the records, but only if
// the stage file hasn't changed since f read it. If the file is locked by
// another command, or it has been changed or removed, the records aren't
// saved. That, or an error saving them, is logged to logger, which may be
// nil.
func (f *File) Check(ctx context.Context, logger *slog.Logger) (*CheckResult, error) {
	result, err := f.stage.Check(ctx)
	if err != nil {
		return nil, err
	}
	if result.Refreshed {
		logger = loggerOrDiscard(logger)
		err := f.saveRefreshed()
		switch {
		case errors.Is(err, ErrLocked), errors.Is(err, errChanged):
			logger.Info("refreshed file stats weren't saved in the stage file", "reason", err)
		case err != nil:
			logger.Warn("couldn't save refreshed file stats in the stage file", "err", err)
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
	if f.lock == "" {
		return errNotLocked
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
// the stage is a draft, it returns [ErrNotFrozen]. The staged files are
// needed only if the commit isn't complete: if they aren't available, and it
// isn't, Resume returns an error wrapping [ocfl.ErrMissingContent] (an
// *[UnavailableError] if staged files have changed) without changing the
// object. If the commit is complete, Resume removes the stage file even if
// the staged files have changed. If Resume returns an
// error, the stage is still frozen: the object may be partially updated. If
// the error wraps [ocfl.ErrUpdateConflict], the object was changed by
// something else after the commit was interrupted, and the commit can't be
// resumed or reverted. Progress is logged to logger, which may be nil.
func (f *File) Resume(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Resume: nil root")
	}
	if f.lock == "" {
		return errNotLocked
	}
	if !f.stage.Frozen() {
		return ErrNotFrozen
	}
	content, closeContent, err := f.stage.localContent(ctx)
	if errors.Is(err, ocfl.ErrMissingContent) {
		// The commit may be complete, with the stage file left behind, so
		// the staged files aren't needed. Apply with no content: if the
		// commit is complete, it does nothing, and if it isn't, it fails
		// without writing anything.
		contentErr := err
		if err := f.apply(ctx, root, nil, logger); err != nil {
			if errors.Is(err, ocfl.ErrMissingContent) {
				return contentErr
			}
			return err
		}
		return nil
	}
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
// resumed. Reverting it again (see [OpenLocked]) finishes reverting. Progress
// is logged to logger, which may be nil.
func (f *File) Revert(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Revert: nil root")
	}
	if f.lock == "" {
		return errNotLocked
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
	if f.lock == "" {
		return errNotLocked
	}
	if err := os.Remove(f.path); err != nil {
		return err
	}
	syncDir(filepath.Dir(f.path))
	return nil
}

// saveRefreshed saves the stage after a check refreshed its records. If f
// doesn't hold the lock, it tries once to take it, and saves the stage only if
// the stage file's contents are the ones f read: otherwise it returns an
// error wrapping errChanged. If the file is locked, it returns an error
// wrapping ErrLocked.
func (f *File) saveRefreshed() (err error) {
	if f.lock != "" {
		return f.Save()
	}
	lock, err := tryLock(f.path)
	if err != nil {
		return err
	}
	f.lock = lock
	defer func() { err = errors.Join(err, f.Close()) }()
	b, err := os.ReadFile(f.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%w: it was removed", errChanged)
	case err != nil:
		return err
	case !bytes.Equal(b, f.saved):
		return errChanged
	}
	return f.Save()
}

// errChanged is returned by saveRefreshed when the stage file has changed
// since the File read it.
var errChanged = errors.New("the stage file was changed by another command")

// read reads the stage file name, without locking it.
func read(name string) (*File, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var s Stage
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("reading stage file %s: %w", name, err)
	}
	return &File{path: name, stage: &s, saved: b}, nil
}

// lockWait is how long lockFile waits for a locked stage file, so a command
// doesn't fail because 'stage status' locked the file briefly to save
// refreshed records.
var lockWait = time.Second

// lockFile locks the stage file name, waiting up to lockWait if it's locked,
// and returns the lock file's name. If it's still locked, it returns an error
// wrapping ErrLocked.
func lockFile(name string) (string, error) {
	deadline := time.Now().Add(lockWait)
	for {
		lock, err := tryLock(name)
		if !errors.Is(err, ErrLocked) || time.Now().After(deadline) {
			return lock, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// tryLock locks the stage file name by creating its lock file, which records
// the process that holds the lock, and returns the lock file's name. If the
// lock file exists, it returns an error wrapping ErrLocked, which names the
// lock file and what it records.
func tryLock(name string) (string, error) {
	lock := name + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		owner := "unknown process"
		if b, err := os.ReadFile(lock); err == nil && len(bytes.TrimSpace(b)) > 0 {
			owner = string(bytes.TrimSpace(b))
		}
		return "", fmt.Errorf("%w (%s): %s exists; if no other ocfl command is using the stage, remove the lock file",
			ErrLocked, owner, lock)
	}
	if err != nil {
		return "", fmt.Errorf("locking the stage file: %w", err)
	}
	host, _ := os.Hostname()
	_, err = fmt.Fprintf(f, "pid %d on %s, since %s\n", os.Getpid(), host, time.Now().Format(time.RFC3339))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(lock)
		return "", fmt.Errorf("locking the stage file: %w", err)
	}
	return lock, nil
}

// unlockFile releases a stage file's lock by removing its lock file, lock.
func unlockFile(lock string) error {
	if err := os.Remove(lock); err != nil {
		return fmt.Errorf("unlocking the stage file: %w; if no other ocfl command is using the stage, remove the lock file %s", err, lock)
	}
	return nil
}

// write writes s as JSON to a temporary file in name's directory, then calls
// place to move it to name, and returns what it wrote. The temporary file is
// removed if it's still there when place returns. The directory is synced
// after place succeeds, so the file isn't lost if the system crashes. If name
// exists, the new file gets its mode; otherwise, it gets mode 0644 less the
// umask.
func write(name string, s *Stage, place func(tmp string) error) ([]byte, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var perm fs.FileMode
	info, err := os.Stat(name)
	switch {
	case err == nil:
		perm = info.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return nil, err
	}
	tmp, err := createTemp(filepath.Dir(name), "."+filepath.Base(name)+".")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name()) // fails after a successful rename
	if _, err := io.Copy(tmp, bytes.NewReader(b)); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if info != nil {
		if err := os.Chmod(tmp.Name(), perm); err != nil {
			return nil, err
		}
	}
	if err := place(tmp.Name()); err != nil {
		return nil, err
	}
	syncDir(filepath.Dir(name))
	return b, nil
}

// createTemp creates a new file in dir, named prefix followed by a random
// string, and opens it for writing. Unlike [os.CreateTemp], which uses mode
// 0600, it creates the file with mode 0644 less the umask, as [os.WriteFile]
// does, without changing the process's umask.
func createTemp(dir, prefix string) (*os.File, error) {
	for range 10000 {
		name := filepath.Join(dir, prefix+rand.Text())
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return f, err
	}
	return nil, &fs.PathError{Op: "createtemp", Path: filepath.Join(dir, prefix+"*"), Err: fs.ErrExist}
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
