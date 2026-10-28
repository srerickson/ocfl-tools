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
	// locked by another command: its lock file, the stage file's name with
	// ".lock" appended, exists. If the command that held the lock was killed,
	// the lock file is left behind: remove it with [RemoveLock].
	ErrLocked = errors.New("the stage file is locked by another command")

	// ErrLockBroken is returned by [File.Close], and by the File methods that
	// change the stage file, when the stage file's lock was removed (see
	// [RemoveLock]) while the File held it: another command may have changed
	// the stage file. The methods check the lock before they change the stage
	// file or the object, so they stop without changing them.
	ErrLockBroken = errors.New("the stage file's lock was removed while the command held it")

	// ErrNotUnlocked is returned by [File.Close] (and by [OpenLocked] and
	// [Create] when they fail and release the lock) when the stage file's lock
	// file couldn't be removed: the stage file stays locked until it's removed
	// with [RemoveLock].
	ErrNotUnlocked = errors.New("the stage file's lock couldn't be released")
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
	saved []byte    // the stage file's contents when it was last read or written
	lock  *heldLock // the lock, if f holds it
}

// Open reads the stage file name without locking it. The File can be read and
// checked, but it can't change the stage file: use [OpenLocked] for that.
func Open(name string) (*File, error) {
	return read(name)
}

// OpenLocked locks the stage file name, then reads it. The lock is held until
// [File.Close] is called. If the stage file is locked by another command,
// OpenLocked waits for a moment (Check may lock it briefly), then returns an
// error wrapping [ErrLocked], which names the lock file. If OpenLocked takes
// the lock, but can't read the stage file, it releases the lock and returns
// the error. If the lock can't be released, the error also wraps
// [ErrNotUnlocked], and the stage file stays locked; if the lock was removed
// in the meantime, it wraps [ErrLockBroken]. Otherwise, if OpenLocked returns
// an error, the lock isn't held.
func OpenLocked(name string) (*File, error) {
	lock, err := lockFile(name)
	if err != nil {
		return nil, err
	}
	f, err := read(name)
	if err != nil {
		return nil, errors.Join(err, lock.release())
	}
	f.lock = lock
	return f, nil
}

// Create locks the new stage file name and saves s in it. The lock is held
// until [File.Close] is called. If the file exists, it isn't replaced: Create
// returns an error that wraps [fs.ErrExist]. If it's locked by another
// command, Create returns an error wrapping [ErrLocked]. If Create takes the
// lock, but can't save the stage, it releases the lock and returns the error.
// If the lock can't be released, the error also wraps [ErrNotUnlocked], and
// the stage file stays locked; if the lock was removed in the meantime, it
// wraps [ErrLockBroken]. Otherwise, if Create returns an error, the lock
// isn't held.
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
		return nil, errors.Join(err, lock.release())
	}
	return &File{path: name, stage: s, saved: b, lock: lock}, nil
}

// LockInfo describes a stage file's lock file, as removed by [RemoveLock].
type LockInfo struct {
	// Path is the lock file: the stage file's name with ".lock" appended.
	Path string

	// Holder is what the lock file records about the command that held the
	// lock (its process ID, host, and when it took the lock), or "" if it
	// couldn't be read.
	Holder string
}

// RemoveLock removes the lock file of the stage file name, releasing the lock
// even if another command holds it. It's for clearing a lock left behind by a
// command that was killed. Removing the lock of a command that's still
// running breaks it: another command can lock and change the stage file at
// the same time. The broken holder's File checks its lock before each change
// to the stage file or the object, and returns an error wrapping
// [ErrLockBroken] instead of making it, but a change that's already under way
// when the lock is removed isn't stopped. Its [File.Close] doesn't remove a
// newer lock, as far as it can tell (see [File.Close]).
//
// RemoveLock returns the lock file's path and what it recorded. If there's no
// lock file, it returns an error wrapping [fs.ErrNotExist], with the path.
// Nothing else is removed: if the lock file isn't a regular file, RemoveLock
// returns an error without removing it. The stage file isn't read or changed,
// and doesn't need to exist.
func RemoveLock(name string) (LockInfo, error) {
	info := LockInfo{Path: lockPath(name)}
	stat, err := os.Lstat(info.Path)
	if err != nil {
		return info, err
	}
	if !stat.Mode().IsRegular() {
		return info, fmt.Errorf("can't remove the stage file lock: %s isn't a regular file", info.Path)
	}
	if b, err := os.ReadFile(info.Path); err == nil {
		info.Holder = lockHolder(b)
	}
	if err := removeLockFile(info.Path); err != nil {
		return info, fmt.Errorf("removing the stage file lock: %w", err)
	}
	syncDir(filepath.Dir(info.Path))
	return info, nil
}

// Path returns the path of the stage file.
func (f *File) Path() string { return f.path }

// Stage returns the stage saved in the file.
func (f *File) Stage() *Stage { return f.stage }

// Close releases the stage file's lock, if f holds it. After Close, f can't
// change the stage file. Calling Close again does nothing.
//
// Close removes the lock file only if it's still the one f created. If it was
// removed while f held the lock (see [RemoveLock]), Close removes nothing and
// returns an error wrapping [ErrLockBroken]: another command may have changed
// the stage file. This is best effort: Close reads the lock file, then
// removes it, so if the lock is removed and taken by another command in
// between, Close removes the other command's lock, and that command sees
// [ErrLockBroken] instead. If the lock file can't be removed, Close returns
// an error wrapping [ErrNotUnlocked], and the stage file stays locked.
func (f *File) Close() error {
	if f.lock == nil {
		return nil
	}
	lock := f.lock
	f.lock = nil
	return lock.release()
}

// Save saves the stage in the file. The file is replaced atomically, so an
// interrupted Save leaves the previous stage file in place.
func (f *File) Save() error {
	if err := f.checkLock(); err != nil {
		return err
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
// was frozen when Commit was called, it returns [ErrFrozen].
//
// Commit checks that f still holds the lock before it saves the frozen stage,
// before it updates the object, and before it removes the stage file. If the
// lock was removed (see [RemoveLock]) before the stage was saved, Commit
// returns an error wrapping [ErrLockBroken] without changing the stage file or
// the object. If it was removed before the object was updated, the error
// wraps ErrLockBroken, and the stage is frozen, but the object isn't changed.
// If it was removed while the object was updated, the commit is complete, but the stage
// file isn't removed: the error wraps [ErrNotRemoved] and [ErrLockBroken].
// Progress is logged to logger, which may be nil.
func (f *File) Commit(ctx context.Context, root *ocfl.Root, msg string, user ocfl.User, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Commit: nil root")
	}
	if f.lock == nil {
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
// resumed or reverted. If the stage file's lock was removed while f held it
// (see [RemoveLock]) before the object is updated, Resume returns an error
// wrapping [ErrLockBroken] without changing the object: it checks the lock
// again after checking the staged files. If the lock is removed while the
// object is updated, the error wraps [ErrNotRemoved] too (see [File.Commit]).
// Progress is logged to logger, which may be nil.
func (f *File) Resume(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Resume: nil root")
	}
	if err := f.checkLock(); err != nil {
		return err
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
// resumed. Reverting it again (see [OpenLocked]) finishes reverting. If the
// stage file's lock was removed while f held it (see [RemoveLock]), Revert
// returns an error wrapping [ErrLockBroken] without changing the object.
// Progress is logged to logger, which may be nil.
func (f *File) Revert(ctx context.Context, root *ocfl.Root, logger *slog.Logger) error {
	if root == nil {
		panic("stage.File.Revert: nil root")
	}
	if err := f.checkLock(); err != nil {
		return err
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
	// checking the staged files may have taken a while
	if err := f.checkLock(); err != nil {
		return err
	}
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

// checkLock returns an error if f doesn't hold the stage file's lock, or if
// the lock was removed while f held it (wrapping ErrLockBroken), so f
// doesn't change the stage file or the object.
func (f *File) checkLock() error {
	if f.lock == nil {
		return errNotLocked
	}
	return f.lock.verify()
}

// remove removes the stage file and syncs its directory.
func (f *File) remove() error {
	if err := f.checkLock(); err != nil {
		return err
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
	if f.lock != nil {
		return f.Save()
	}
	lock, busy, err := tryLock(f.path)
	if busy {
		// don't wait for the lock, or read the lock file: the other command
		// may be removing it
		return fmt.Errorf("%w: %v", ErrLocked, err)
	}
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

const (
	// unlockWait is how long removeLockFile retries removing a lock file that's
	// open in another process, which on Windows prevents removing it.
	unlockWait = time.Second

	// lockRetry is the delay between attempts to create or remove a lock
	// file.
	lockRetry = 50 * time.Millisecond
)

// heldLock is a stage file lock held by this process: the lock file it
// created, and the contents it wrote, which identify it. Each lock file
// records a random token, so a lock file created by another command after this
// one was removed doesn't have the same contents, even if it has the same
// holder and time, or reuses the removed file's inode.
type heldLock struct {
	path     string
	contents []byte
}

// verify returns an error wrapping ErrLockBroken if l's lock file isn't the
// one that l created: it was removed, and maybe created again by another
// command. On systems where a file that's being removed can't be read, it
// retries for up to unlockWait, then returns ErrLockBroken.
func (l *heldLock) verify() error {
	deadline := time.Now().Add(unlockWait)
	for {
		b, err := os.ReadFile(l.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("%w: %s was removed", ErrLockBroken, l.path)
		case err != nil && removeBusy(err):
			if time.Now().After(deadline) {
				return fmt.Errorf("%w: %s is being removed: %w", ErrLockBroken, l.path, err)
			}
			time.Sleep(lockRetry)
			continue
		case err != nil:
			return fmt.Errorf("checking the stage file lock: %w", err)
		case !bytes.Equal(b, l.contents):
			return fmt.Errorf("%w: %s was removed, and another command (%s) has locked the stage file since",
				ErrLockBroken, l.path, lockHolder(b))
		}
		return nil
	}
}

// release releases the lock by removing its lock file, if it's still the one
// that l created (see verify). Otherwise, it returns an error wrapping
// ErrLockBroken. If the lock file can't be read or removed, it returns an
// error wrapping ErrNotUnlocked.
func (l *heldLock) release() error {
	if err := l.verify(); err != nil {
		if errors.Is(err, ErrLockBroken) {
			return err
		}
		return fmt.Errorf("%w: %w", ErrNotUnlocked, err)
	}
	if err := removeLockFile(l.path); err != nil {
		return fmt.Errorf("%w: %w", ErrNotUnlocked, err)
	}
	return nil
}

// lockPath returns the name of the lock file of the stage file name.
func lockPath(name string) string { return name + ".lock" }

// lockHolder returns what the contents b of a lock file record about the
// command that holds the lock: its first line. It's "" if there's nothing.
func lockHolder(b []byte) string {
	line, _, _ := bytes.Cut(b, []byte("\n"))
	return string(bytes.TrimSpace(line))
}

// lockFile locks the stage file name, waiting up to lockWait if it's locked.
// If it's still locked, it returns an error wrapping ErrLocked, which says
// what the lock file records. The lock file is only read then, so waiting
// doesn't keep it open while its holder removes it. If it was removed just
// before it's read, lockFile tries once more to lock the stage file.
func lockFile(name string) (*heldLock, error) {
	deadline := time.Now().Add(lockWait)
	retried := false
	for {
		lock, busy, err := tryLock(name)
		if !busy {
			return lock, err
		}
		if time.Now().After(deadline) {
			path := lockPath(name)
			b, readErr := os.ReadFile(path)
			if errors.Is(readErr, fs.ErrNotExist) && !retried {
				retried = true
				continue
			}
			return nil, lockedError(path, b, readErr, err)
		}
		time.Sleep(lockRetry)
	}
}

// tryLock tries once to lock the stage file name by creating its lock file,
// which records the process that holds the lock and a random token. If
// another command holds the lock, or is releasing it, tryLock returns busy,
// with the error from creating the lock file.
func tryLock(name string) (lock *heldLock, busy bool, err error) {
	path := lockPath(name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if lockBusy(err) {
			return nil, true, err
		}
		return nil, false, fmt.Errorf("locking the stage file: %w", err)
	}
	// the lock file is a line describing the holder, then a line with the
	// token that identifies this lock
	host, _ := os.Hostname()
	contents := fmt.Appendf(nil, "pid %d on %s, since %s\n%s\n",
		os.Getpid(), host, time.Now().Format(time.RFC3339), rand.Text())
	_, err = f.Write(contents)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return nil, false, fmt.Errorf("locking the stage file: %w", err)
	}
	return &heldLock{path: path, contents: contents}, false, nil
}

// lockedError returns the error for the stage file lock lock, which couldn't
// be created because of err, given the result of reading it, b and readErr:
// an error wrapping ErrLocked, which names the lock file and says what it
// records. If the lock file doesn't exist, the lock wasn't held by another
// command, and lockedError returns err.
func lockedError(lock string, b []byte, readErr, err error) error {
	if errors.Is(readErr, fs.ErrNotExist) && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("locking the stage file: %w", err)
	}
	owner := "unknown process"
	if readErr == nil && lockHolder(b) != "" {
		owner = lockHolder(b)
	}
	return fmt.Errorf("%w (%s): %s exists", ErrLocked, owner, lock)
}

// removeLockFile removes the lock file lock. If it's open in another
// process, which prevents removing it on some systems, removeLockFile retries
// for up to unlockWait.
func removeLockFile(lock string) error {
	deadline := time.Now().Add(unlockWait)
	for {
		err := os.Remove(lock)
		if err == nil || !removeBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(lockRetry)
	}
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
