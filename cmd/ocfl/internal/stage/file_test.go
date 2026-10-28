package stage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go"
	ocflfs "github.com/srerickson/ocfl-go/fs"
	"github.com/srerickson/ocfl-go/fs/local"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
)

func TestOpen(t *testing.T) {
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t,
		"testdata/content-fixture",
		"testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root",
	)
	root, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[1]), ".")
	be.NilErr(t, err)
	s := newStage(t, root, "ark:123/abc")
	be.NilErr(t, s.AddDir(ctx, fixtures[0], nil))
	dir := t.TempDir()
	name := filepath.Join(dir, "stage.json")
	f, err := stage.Create(name, s)
	be.NilErr(t, err)
	be.Equal(t, name, f.Path())

	t.Run("draft", func(t *testing.T) {
		got, err := stage.Open(name)
		be.NilErr(t, err)
		be.Equal(t, name, got.Path())
		gs := got.Stage()
		be.False(t, gs.Frozen())
		be.Equal(t, testRootLoc, gs.Root())
		be.True(t, gs.Update().NewState().Eq(s.Update().NewState()))
		for p := range s.Update().NewState().PathMap() {
			want, wantOK := s.SourceFile(p)
			src, ok := gs.SourceFile(p)
			be.Equal(t, wantOK, ok)
			be.Equal(t, want, src)
		}
		// files that were added aren't read again
		result, err := gs.Check(ctx)
		be.NilErr(t, err)
		be.Equal(t, 0, len(result.Changed))
	})

	t.Run("frozen", func(t *testing.T) {
		// the root is read-only, so the commit fails after the stage is
		// frozen and saved
		err := f.Commit(ctx, root, "message", ocfl.User{Name: "name"}, nil)
		be.Nonzero(t, err)
		be.True(t, s.Frozen())
		got, err := stage.Open(name)
		be.NilErr(t, err)
		be.True(t, got.Stage().Frozen())
		be.Equal(t, s.Update().NewInventoryDigest(), got.Stage().Update().NewInventoryDigest())
	})

	t.Run("no temp files left", func(t *testing.T) {
		be.NilErr(t, f.Close())
		entries, err := os.ReadDir(dir)
		be.NilErr(t, err)
		be.Equal(t, 1, len(entries))
		be.Equal(t, "stage.json", entries[0].Name())
	})

	t.Run("invalid", func(t *testing.T) {
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, []byte(`{"object_id":"old stage file"}`), 0644))
		_, err := stage.Open(bad)
		be.Nonzero(t, err)
	})

	t.Run("no root", func(t *testing.T) {
		var saved map[string]any
		b, err := json.Marshal(s)
		be.NilErr(t, err)
		be.NilErr(t, json.Unmarshal(b, &saved))
		delete(saved, "root")
		b, err = json.Marshal(saved)
		be.NilErr(t, err)
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, b, 0644))
		_, err = stage.Open(bad)
		be.In(t, "no storage root", err.Error())
	})

	t.Run("staged file doesn't match update", func(t *testing.T) {
		var saved map[string]any
		b, err := json.Marshal(s)
		be.NilErr(t, err)
		be.NilErr(t, json.Unmarshal(b, &saved))
		files := saved["files"].(map[string]any)
		files["hello.csv"].(map[string]any)["digest"] = strings.Repeat("0", 128)
		b, err = json.Marshal(saved)
		be.NilErr(t, err)
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, b, 0644))
		_, err = stage.Open(bad)
		be.In(t, "hello.csv", err.Error())
	})
}

func TestOpenLocked(t *testing.T) {
	ctx := context.Background()
	stage.SetLockWait(t, 100*time.Millisecond)
	root := newRoot(t)

	t.Run("lock held until close", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		lock := f.Path() + ".lock"
		_, err := os.Stat(lock)
		be.NilErr(t, err)
		// another command can't lock the file
		_, err = stage.OpenLocked(f.Path())
		be.True(t, errors.Is(err, stage.ErrLocked))
		be.In(t, lock+" exists", err.Error())
		be.In(t, fmt.Sprintf("pid %d", os.Getpid()), err.Error())
		// but it can read it
		got, err := stage.Open(f.Path())
		be.NilErr(t, err)
		be.Equal(t, "ark:123/abc", got.Stage().ID())
		be.NilErr(t, f.Close())
		be.NilErr(t, f.Close()) // closing again does nothing
		lockRemoved(t, f)
		// a closed file can't be changed
		be.Nonzero(t, f.Save())
		be.Nonzero(t, f.Delete())
		locked, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, locked.Save())
		be.NilErr(t, locked.Close())
		lockRemoved(t, f)
	})

	t.Run("waits for a brief lock", func(t *testing.T) {
		stage.SetLockWait(t, 10*time.Second)
		f := newFile(t, root, "ark:123/abc")
		closed := make(chan error)
		time.AfterFunc(100*time.Millisecond, func() { closed <- f.Close() })
		locked, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, <-closed)
		be.NilErr(t, locked.Close())
	})

	t.Run("stale lock", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Close())
		lock := f.Path() + ".lock"
		be.NilErr(t, os.WriteFile(lock, nil, 0o644))
		_, err := stage.OpenLocked(f.Path())
		be.True(t, errors.Is(err, stage.ErrLocked))
		be.In(t, "unknown process", err.Error())
		// the lock file isn't removed
		_, err = os.Stat(lock)
		be.NilErr(t, err)
	})

	t.Run("lock can't be released", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		lock, unlock := testutil.LockDir(t, filepath.Dir(f.Path()))
		lock()
		err := f.Close()
		unlock()
		be.True(t, errors.Is(err, stage.ErrNotUnlocked))
		// the stage file is still locked
		_, err = stage.OpenLocked(f.Path())
		be.True(t, errors.Is(err, stage.ErrLocked))
	})

	t.Run("unlocked file can't be changed", func(t *testing.T) {
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"a.txt": "a"}), nil))
		be.NilErr(t, f.Save())
		be.NilErr(t, f.Close())
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		got, err := stage.Open(f.Path())
		be.NilErr(t, err)
		be.Nonzero(t, got.Save())
		be.Nonzero(t, got.Delete())
		be.Nonzero(t, got.Discard())
		be.Nonzero(t, got.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		be.False(t, got.Stage().Frozen())
		be.Nonzero(t, got.Resume(ctx, writable, nil))
		be.Nonzero(t, got.Revert(ctx, writable, nil))
		after, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		be.Equal(t, string(before), string(after))
		lockRemoved(t, f)
	})

	t.Run("lock released if the file can't be read", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "missing.json")
		_, err := stage.OpenLocked(missing)
		be.True(t, errors.Is(err, fs.ErrNotExist))
		bad := filepath.Join(dir, "bad.json")
		be.NilErr(t, os.WriteFile(bad, []byte("{}"), 0o644))
		_, err = stage.OpenLocked(bad)
		be.Nonzero(t, err)
		entries, err := os.ReadDir(dir)
		be.NilErr(t, err)
		be.Equal(t, 1, len(entries))
	})
}

func TestRemoveLock(t *testing.T) {
	stage.SetLockWait(t, 0)
	root := newRoot(t)

	// removed checks that the lock file of f was removed, and nothing else in
	// its directory was changed: the stage file has its contents before.
	removed := func(t *testing.T, f *stage.File, before []byte) {
		t.Helper()
		lockRemoved(t, f)
		after, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		be.Equal(t, string(before), string(after))
		entries, err := os.ReadDir(filepath.Dir(f.Path()))
		be.NilErr(t, err)
		be.Equal(t, 1, len(entries))
	}

	t.Run("held lock", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		info, err := stage.RemoveLock(f.Path())
		be.NilErr(t, err)
		be.Equal(t, f.Path()+".lock", info.Path)
		// the holder is the first line, without the lock's token
		be.True(t, strings.HasPrefix(info.Holder, fmt.Sprintf("pid %d on ", os.Getpid())))
		be.False(t, strings.Contains(info.Holder, "\n"))
		removed(t, f, before)
		// the broken holder's Close reports it
		be.True(t, errors.Is(f.Close(), stage.ErrLockBroken))
		// the stage file can be locked again
		locked, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, locked.Close())
	})

	// A command (a) holds the lock when it's removed, and another command (b)
	// locks the stage file before a finishes. a's Close doesn't remove b's
	// lock, so a third command (c) can't lock the stage file while b runs.
	t.Run("broken lock", func(t *testing.T) {
		a := newFile(t, root, "ark:123/abc")
		_, err := stage.RemoveLock(a.Path())
		be.NilErr(t, err)
		b, err := stage.OpenLocked(a.Path())
		be.NilErr(t, err)
		err = a.Close()
		be.True(t, errors.Is(err, stage.ErrLockBroken))
		be.In(t, fmt.Sprintf("another command (pid %d on ", os.Getpid()), err.Error())
		_, err = os.Stat(a.Path() + ".lock")
		be.NilErr(t, err)
		_, err = stage.OpenLocked(a.Path())
		be.True(t, errors.Is(err, stage.ErrLocked))
		be.NilErr(t, b.Save())
		be.NilErr(t, b.Close())
		lockRemoved(t, a)
	})

	// After its lock is removed, the broken holder doesn't change the stage
	// file or the object.
	t.Run("broken holder stops, draft", func(t *testing.T) {
		ctx := context.Background()
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"new.txt": "new"}), nil))
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		_, err = stage.RemoveLock(f.Path())
		be.NilErr(t, err)
		be.True(t, errors.Is(f.Save(), stage.ErrLockBroken))
		be.True(t, errors.Is(f.Delete(), stage.ErrLockBroken))
		be.True(t, errors.Is(f.Discard(), stage.ErrLockBroken))
		be.True(t, errors.Is(f.Commit(ctx, writable, "message", ocfl.User{Name: "name"}, nil), stage.ErrLockBroken))
		removed(t, f, before)
		obj, err := readOnly.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
		be.True(t, errors.Is(f.Close(), stage.ErrLockBroken))
	})

	t.Run("broken holder stops, frozen", func(t *testing.T) {
		ctx := context.Background()
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"new.txt": "new"}), nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		be.NilErr(t, f.Close())
		frozen, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		_, err = stage.RemoveLock(f.Path())
		be.NilErr(t, err)
		be.True(t, errors.Is(frozen.Resume(ctx, writable, nil), stage.ErrLockBroken))
		be.True(t, errors.Is(frozen.Revert(ctx, writable, nil), stage.ErrLockBroken))
		removed(t, frozen, before)
		obj, err := readOnly.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
		be.True(t, errors.Is(frozen.Close(), stage.ErrLockBroken))
	})

	t.Run("stale lock", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Close())
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		lock := f.Path() + ".lock"
		be.NilErr(t, os.WriteFile(lock, []byte("pid 1 on other-host\n"), 0o644))
		_, err = stage.OpenLocked(f.Path())
		be.True(t, errors.Is(err, stage.ErrLocked))
		info, err := stage.RemoveLock(f.Path())
		be.NilErr(t, err)
		be.Equal(t, "pid 1 on other-host", info.Holder)
		removed(t, f, before)
		// a later write succeeds
		locked, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, locked.Save())
		be.NilErr(t, locked.Close())
		lockRemoved(t, f)
	})

	t.Run("empty lock file", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Close())
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, os.WriteFile(f.Path()+".lock", nil, 0o644))
		info, err := stage.RemoveLock(f.Path())
		be.NilErr(t, err)
		be.Equal(t, "", info.Holder)
		removed(t, f, before)
	})

	t.Run("no lock", func(t *testing.T) {
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Close())
		info, err := stage.RemoveLock(f.Path())
		be.True(t, errors.Is(err, fs.ErrNotExist))
		be.Equal(t, f.Path()+".lock", info.Path)
	})

	t.Run("no stage file", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "stage.json")
		lock := name + ".lock"
		be.NilErr(t, os.WriteFile(lock, []byte("pid 1 on other-host\n"), 0o644))
		info, err := stage.RemoveLock(name)
		be.NilErr(t, err)
		be.Equal(t, "pid 1 on other-host", info.Holder)
		_, err = os.Stat(lock)
		be.True(t, errors.Is(err, fs.ErrNotExist))
	})

	t.Run("lock isn't a regular file", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "stage.json")
		lock := name + ".lock"
		be.NilErr(t, os.Mkdir(lock, 0o755))
		_, err := stage.RemoveLock(name)
		be.Nonzero(t, err)
		be.False(t, errors.Is(err, fs.ErrNotExist))
		_, err = os.Stat(lock)
		be.NilErr(t, err)
	})
}

func TestCreate(t *testing.T) {
	root := newRoot(t)
	dir := t.TempDir()
	name := filepath.Join(dir, "stage.json")
	f, err := stage.Create(name, newStage(t, root, "ark:123/abc"))
	be.NilErr(t, err)
	be.NilErr(t, f.Close())
	got, err := stage.Open(name)
	be.NilErr(t, err)
	be.Equal(t, "ark:123/abc", got.Stage().ID())
	// an existing file isn't replaced
	_, err = stage.Create(name, newStage(t, root, "ark:xyz/987"))
	be.True(t, errors.Is(err, fs.ErrExist))
	got, err = stage.Open(name)
	be.NilErr(t, err)
	be.Equal(t, "ark:123/abc", got.Stage().ID())
	// no temp or lock files left
	entries, err := os.ReadDir(dir)
	be.NilErr(t, err)
	be.Equal(t, 1, len(entries))
	// a locked file isn't created
	stage.SetLockWait(t, 0)
	locked := filepath.Join(dir, "locked.json")
	be.NilErr(t, os.WriteFile(locked+".lock", nil, 0o644))
	_, err = stage.Create(locked, newStage(t, root, "ark:123/abc"))
	be.True(t, errors.Is(err, stage.ErrLocked))
	_, err = os.Stat(locked)
	be.True(t, errors.Is(err, fs.ErrNotExist))
}

// TestCreate_mode isn't parallel: it sets the process's umask.
func TestCreate_mode(t *testing.T) {
	root := newRoot(t)
	for _, tt := range []struct {
		umask int
		want  fs.FileMode
	}{
		{umask: 0o022, want: 0o644},
		{umask: 0o077, want: 0o600},
	} {
		testutil.SetUmask(t, tt.umask)
		name := filepath.Join(t.TempDir(), "stage.json")
		_, err := stage.Create(name, newStage(t, root, "ark:123/abc"))
		be.NilErr(t, err)
		info, err := os.Stat(name)
		be.NilErr(t, err)
		be.Equal(t, tt.want, info.Mode().Perm())
	}
}

func TestFile_Save(t *testing.T) {
	ctx := context.Background()
	f := newFile(t, newRoot(t), "ark:123/abc")
	dir := writeFiles(t, map[string]string{"new.txt": "new"})
	be.NilErr(t, f.Stage().AddDir(ctx, dir, nil))
	be.NilErr(t, f.Save())
	got, err := stage.Open(f.Path())
	be.NilErr(t, err)
	be.Equal(t, "a_file.txt\nnew.txt\n", list(got.Stage()))
}

// TestFile_Save_mode isn't parallel: it sets the process's umask.
func TestFile_Save_mode(t *testing.T) {
	testutil.SetUmask(t, 0o022)
	f := newFile(t, newRoot(t), "ark:123/abc")
	// the mode of the existing file is kept, whatever the umask
	be.NilErr(t, os.Chmod(f.Path(), 0o640))
	testutil.SetUmask(t, 0o077)
	be.NilErr(t, f.Save())
	info, err := os.Stat(f.Path())
	be.NilErr(t, err)
	be.Equal(t, 0o640, info.Mode().Perm())
}

func TestFile_Check(t *testing.T) {
	ctx := context.Background()
	dir := writeFiles(t, map[string]string{"a.txt": "a"})
	f := newFile(t, newRoot(t), "ark:123/abc")
	be.NilErr(t, f.Stage().AddDir(ctx, dir, nil))
	be.NilErr(t, f.Save())
	// the file's stat changes, but its content doesn't
	mtime := time.Now().Add(-time.Hour)
	be.NilErr(t, os.Chtimes(filepath.Join(dir, "a.txt"), mtime, mtime))
	before, err := os.ReadFile(f.Path())
	be.NilErr(t, err)
	result, err := f.Check(ctx, nil)
	be.NilErr(t, err)
	be.Equal(t, 0, len(result.Changed))
	be.True(t, result.Refreshed)
	// the refreshed stat is saved
	after, err := os.ReadFile(f.Path())
	be.NilErr(t, err)
	be.Unequal(t, string(before), string(after))
	got, err := stage.Open(f.Path())
	be.NilErr(t, err)
	result, err = got.Check(ctx, nil)
	be.NilErr(t, err)
	be.False(t, result.Refreshed)
}

// TestFile_Check_Unlocked tests saving refreshed records from a stage file
// that was opened without its lock, as by 'stage status', while another
// command changes the stage file.
func TestFile_Check_Unlocked(t *testing.T) {
	ctx := context.Background()
	stage.SetLockWait(t, 0)
	// setup returns a stage file, which isn't locked, with a staged file
	// whose stat has changed since it was added, so checking it refreshes
	// the stage, and read-only and writable roots for the stage.
	setup := func(t *testing.T) (f *stage.File, readOnly, writable *ocfl.Root) {
		readOnly, writable = newRoots(t)
		dir := writeFiles(t, map[string]string{"a.txt": "a"})
		created := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, created.Stage().AddDir(ctx, dir, nil))
		be.NilErr(t, created.Save())
		be.NilErr(t, created.Close())
		mtime := time.Now().Add(-time.Hour)
		be.NilErr(t, os.Chtimes(filepath.Join(dir, "a.txt"), mtime, mtime))
		f, err := stage.Open(created.Path())
		be.NilErr(t, err)
		return f, readOnly, writable
	}
	// check checks f, which is refreshed, but whose refreshed records
	// shouldn't be saved: the stage file should have want.
	check := func(t *testing.T, f *stage.File, want []byte) {
		t.Helper()
		result, err := f.Check(ctx, nil)
		be.NilErr(t, err)
		be.True(t, result.Refreshed)
		be.Equal(t, 0, len(result.Changed))
		got, err := os.ReadFile(f.Path())
		if want == nil {
			be.True(t, errors.Is(err, fs.ErrNotExist))
			return
		}
		be.NilErr(t, err)
		be.Equal(t, string(want), string(got))
	}

	t.Run("unchanged file", func(t *testing.T) {
		f, _, _ := setup(t)
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		result, err := f.Check(ctx, nil)
		be.NilErr(t, err)
		be.True(t, result.Refreshed)
		after, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		be.Unequal(t, string(before), string(after))
		lockRemoved(t, f)
		// the stage file can be locked again
		locked, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, locked.Close())
	})

	t.Run("frozen after it was read", func(t *testing.T) {
		f, readOnly, writable := setup(t)
		// another command freezes the stage, and the commit is interrupted
		other, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.Nonzero(t, other.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		be.NilErr(t, other.Close())
		frozen, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		check(t, f, frozen)
		lockRemoved(t, f)
		// the commit can still be reverted
		reverting, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.True(t, reverting.Stage().Frozen())
		be.NilErr(t, reverting.Revert(ctx, writable, nil))
		be.NilErr(t, reverting.Close())
	})

	t.Run("changed while locked", func(t *testing.T) {
		f, readOnly, _ := setup(t)
		// another command is committing the stage
		other, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		defer other.Close()
		be.Nonzero(t, other.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		frozen, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		check(t, f, frozen)
		// the other command's lock is kept
		be.True(t, other.Stage().Frozen())
		_, err = os.Stat(f.Path() + ".lock")
		be.NilErr(t, err)
		be.NilErr(t, other.Save())
	})

	t.Run("locked", func(t *testing.T) {
		f, _, _ := setup(t)
		other, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		before, err := os.ReadFile(f.Path())
		be.NilErr(t, err)
		check(t, f, before)
		_, err = os.Stat(f.Path() + ".lock")
		be.NilErr(t, err)
		be.NilErr(t, other.Close())
	})

	t.Run("removed after it was read", func(t *testing.T) {
		f, _, writable := setup(t)
		// another command commits the stage, removing the stage file
		other, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		be.NilErr(t, other.Commit(ctx, writable, "message", ocfl.User{Name: "name"}, nil))
		be.NilErr(t, other.Close())
		check(t, f, nil)
		lockRemoved(t, f)
	})
}

func TestFile_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("draft", func(t *testing.T) {
		f := newFile(t, newRoot(t), "ark:123/abc")
		be.NilErr(t, f.Delete())
		stageFileRemoved(t, f)
	})

	t.Run("frozen", func(t *testing.T) {
		readOnly, _ := newRoots(t)
		f := newFile(t, readOnly, "ark:xyz/987")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"a.txt": "a"}), nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		be.True(t, errors.Is(f.Delete(), stage.ErrFrozen))
		_, err := os.Stat(f.Path())
		be.NilErr(t, err)
		// a frozen stage can be discarded
		be.NilErr(t, f.Discard())
		stageFileRemoved(t, f)
	})
}

func TestFile_Commit(t *testing.T) {
	ctx := context.Background()
	user := ocfl.User{Name: "name"}
	content := map[string]string{"a.txt": "a", "b.txt": "b"}
	// setup returns a stage file for a new version of the object ark:123/abc
	// with content in a new directory, and read-only and writable roots for
	// the same storage root.
	setup := func(t *testing.T) (f *stage.File, dir string, readOnly, writable *ocfl.Root) {
		readOnly, writable = newRoots(t)
		f = newFile(t, writable, "ark:123/abc")
		dir = writeFiles(t, content)
		be.NilErr(t, f.Stage().AddDir(ctx, dir, nil))
		be.NilErr(t, f.Save())
		return f, dir, readOnly, writable
	}

	t.Run("commit", func(t *testing.T) {
		f, _, _, root := setup(t)
		be.NilErr(t, f.Commit(ctx, root, "message", user, nil))
		stageFileRemoved(t, f)
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
	})

	t.Run("unavailable content", func(t *testing.T) {
		f, dir, _, root := setup(t)
		be.NilErr(t, os.Remove(filepath.Join(dir, "a.txt")))
		err := f.Commit(ctx, root, "message", user, nil)
		be.True(t, errors.Is(err, ocfl.ErrMissingContent))
		be.In(t, "a.txt", err.Error())
		// the stage is still a draft, so it can be changed
		be.False(t, f.Stage().Frozen())
		be.False(t, savedStage(t, f).Frozen())
	})

	t.Run("apply fails", func(t *testing.T) {
		f, _, readOnly, _ := setup(t)
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", user, nil))
		// the frozen stage is saved before the object is changed
		be.True(t, f.Stage().Frozen())
		saved := savedStage(t, f)
		be.True(t, saved.Frozen())
		be.Equal(t, f.Stage().Update().NewInventoryDigest(), saved.Update().NewInventoryDigest())
		// a frozen stage can't be committed again
		be.True(t, errors.Is(f.Commit(ctx, readOnly, "message", user, nil), stage.ErrFrozen))
	})

	t.Run("object changed", func(t *testing.T) {
		f, _, _, root := setup(t)
		// another stage commits a new version first
		other := newFile(t, root, "ark:123/abc")
		be.NilErr(t, other.Stage().AddDir(ctx, writeFiles(t, map[string]string{"other.txt": "other"}), nil))
		be.NilErr(t, other.Commit(ctx, root, "other", user, nil))
		err := f.Commit(ctx, root, "message", user, nil)
		be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
		// nothing was written, and the stage is a draft again
		be.False(t, f.Stage().Frozen())
		saved := savedStage(t, f)
		be.False(t, saved.Frozen())
		be.Equal(t, "a.txt\na_file.txt\nb.txt\n", list(saved))
		be.NilErr(t, f.Delete())
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
	})

	t.Run("stage file not saved", func(t *testing.T) {
		f, _, _, root := setup(t)
		lock, _ := testutil.LockDir(t, filepath.Dir(f.Path()))
		lock()
		be.Nonzero(t, f.Commit(ctx, root, "message", user, nil))
		// the object isn't changed, and the stage is still a draft
		be.False(t, f.Stage().Frozen())
		be.False(t, savedStage(t, f).Frozen())
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
	})

	t.Run("object changed, draft not saved", func(t *testing.T) {
		root, fsys := newHookRoot(t)
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, content), nil))
		other := newFile(t, root, "ark:123/abc")
		be.NilErr(t, other.Stage().AddDir(ctx, writeFiles(t, map[string]string{"other.txt": "other"}), nil))
		be.NilErr(t, other.Commit(ctx, root, "other", user, nil))
		// the stage file can't be changed once the object is read
		lock, _ := testutil.LockDir(t, filepath.Dir(f.Path()))
		fsys.beforeOpen = sync.OnceFunc(lock)
		err := f.Commit(ctx, root, "message", user, nil)
		be.True(t, errors.Is(err, ocfl.ErrUpdateConflict))
		var notSaved *stage.DraftNotSavedError
		be.True(t, errors.As(err, &notSaved))
		be.True(t, errors.Is(notSaved.Err, ocfl.ErrUpdateConflict))
		be.True(t, errors.Is(notSaved.SaveErr, fs.ErrPermission))
		// the stage is frozen, like the stage in the file
		be.True(t, f.Stage().Frozen())
		saved := savedStage(t, f)
		be.True(t, saved.Frozen())
		be.Equal(t, f.Stage().Update().NewInventoryDigest(), saved.Update().NewInventoryDigest())
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
	})

	t.Run("stage file not removed", func(t *testing.T) {
		root, fsys := newHookRoot(t)
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, content), nil))
		// the stage file can't be removed once the object is changed
		lock, unlock := testutil.LockDir(t, filepath.Dir(f.Path()))
		fsys.beforeWrite = sync.OnceFunc(lock)
		err := f.Commit(ctx, root, "message", user, nil)
		be.True(t, errors.Is(err, stage.ErrNotRemoved))
		be.True(t, errors.Is(err, fs.ErrPermission))
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
		// the commit is complete, but the stage is frozen, like the stage in
		// the file, and resuming the commit removes the file
		be.True(t, f.Stage().Frozen())
		be.True(t, savedStage(t, f).Frozen())
		unlock()
		be.NilErr(t, f.Resume(ctx, root, nil))
		stageFileRemoved(t, f)
		obj, err = root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
	})

	t.Run("stage file removed during commit", func(t *testing.T) {
		root, fsys := newHookRoot(t)
		f := newFile(t, root, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, content), nil))
		fsys.beforeWrite = sync.OnceFunc(func() {
			if err := os.Remove(f.Path()); err != nil {
				t.Error(err)
			}
		})
		be.NilErr(t, f.Commit(ctx, root, "message", user, nil))
		stageFileRemoved(t, f)
		obj, err := root.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
	})
}

func TestFile_Resume(t *testing.T) {
	ctx := context.Background()
	user := ocfl.User{Name: "name"}

	t.Run("resume", func(t *testing.T) {
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"a.txt": "a"}), nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", user, nil))
		be.NilErr(t, f.Close())
		saved, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		defer saved.Close()
		be.NilErr(t, saved.Resume(ctx, writable, nil))
		stageFileRemoved(t, f)
		obj, err := writable.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(2), obj.Head())
		// the saved version metadata is used
		be.Equal(t, "message", obj.Version(0).Message())
	})

	t.Run("draft", func(t *testing.T) {
		_, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.True(t, errors.Is(f.Resume(ctx, writable, nil), stage.ErrNotFrozen))
		be.False(t, savedStage(t, f).Frozen())
	})

	t.Run("unavailable content", func(t *testing.T) {
		readOnly, writable := newRoots(t)
		dir := writeFiles(t, map[string]string{"a.txt": "a"})
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, dir, nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", user, nil))
		be.NilErr(t, os.Remove(filepath.Join(dir, "a.txt")))
		err := f.Resume(ctx, writable, nil)
		be.True(t, errors.Is(err, ocfl.ErrMissingContent))
		var unavailable *stage.UnavailableError
		be.True(t, errors.As(err, &unavailable))
		be.AllEqual(t, []string{"a.txt"}, unavailable.Paths)
		// the commit is still in progress, and the object wasn't changed
		be.True(t, savedStage(t, f).Frozen())
		objPath, err := writable.ResolveID("ark:123/abc")
		be.NilErr(t, err)
		_, err = ocflfs.ReadDir(ctx, writable.FS(), path.Join(writable.Path(), objPath, "v2"))
		be.True(t, errors.Is(err, fs.ErrNotExist))
		obj, err := writable.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
	})

	// the commit is complete, but the stage file wasn't removed, and the
	// staged files changed afterwards: they aren't needed to resume it
	for _, tt := range []struct {
		desc   string
		change func(name string) error
	}{
		{"staged file deleted", os.Remove},
		{"staged file modified", func(name string) error {
			return os.WriteFile(name, []byte("changed"), 0o644)
		}},
	} {
		t.Run("complete, "+tt.desc, func(t *testing.T) {
			root, fsys := newHookRoot(t)
			dir := writeFiles(t, map[string]string{"a.txt": "a"})
			f := newFile(t, root, "ark:123/abc")
			be.NilErr(t, f.Stage().AddDir(ctx, dir, nil))
			lock, unlock := testutil.LockDir(t, filepath.Dir(f.Path()))
			fsys.beforeWrite = sync.OnceFunc(lock)
			be.True(t, errors.Is(f.Commit(ctx, root, "message", user, nil), stage.ErrNotRemoved))
			unlock()
			be.NilErr(t, tt.change(filepath.Join(dir, "a.txt")))
			be.NilErr(t, f.Close())
			saved, err := stage.OpenLocked(f.Path())
			be.NilErr(t, err)
			defer saved.Close()
			be.NilErr(t, saved.Resume(ctx, root, nil))
			stageFileRemoved(t, f)
			obj, err := root.NewObject(ctx, "ark:123/abc")
			be.NilErr(t, err)
			be.Equal(t, ocfl.V(2), obj.Head())
		})
	}
}

func TestFile_Revert(t *testing.T) {
	ctx := context.Background()

	t.Run("revert", func(t *testing.T) {
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"a.txt": "a"}), nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		be.NilErr(t, f.Close())
		saved, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		defer saved.Close()
		be.NilErr(t, saved.Revert(ctx, writable, nil))
		// the stage is saved as a draft with the staged files
		be.False(t, saved.Stage().Frozen())
		draft := savedStage(t, f)
		be.False(t, draft.Frozen())
		be.Equal(t, "a.txt\na_file.txt\n", list(draft))
		obj, err := writable.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
	})

	t.Run("draft", func(t *testing.T) {
		_, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.True(t, errors.Is(f.Revert(ctx, writable, nil), stage.ErrNotFrozen))
	})

	t.Run("draft not saved", func(t *testing.T) {
		readOnly, writable := newRoots(t)
		f := newFile(t, writable, "ark:123/abc")
		be.NilErr(t, f.Stage().AddDir(ctx, writeFiles(t, map[string]string{"a.txt": "a"}), nil))
		be.Nonzero(t, f.Commit(ctx, readOnly, "message", ocfl.User{Name: "name"}, nil))
		lock, unlock := testutil.LockDir(t, filepath.Dir(f.Path()))
		lock()
		err := f.Revert(ctx, writable, nil)
		be.In(t, "reverted", err.Error())
		be.True(t, errors.Is(err, fs.ErrPermission))
		// the commit is reverted and the stage is a draft, but the file still
		// has the frozen stage
		be.False(t, f.Stage().Frozen())
		be.True(t, savedStage(t, f).Frozen())
		obj, err := writable.NewObject(ctx, "ark:123/abc")
		be.NilErr(t, err)
		be.Equal(t, ocfl.V(1), obj.Head())
		// reverting the stage in the file again finishes reverting
		unlock()
		be.NilErr(t, f.Close())
		saved, err := stage.OpenLocked(f.Path())
		be.NilErr(t, err)
		defer saved.Close()
		be.NilErr(t, saved.Revert(ctx, writable, nil))
		draft := savedStage(t, f)
		be.False(t, draft.Frozen())
		be.Equal(t, "a.txt\na_file.txt\n", list(draft))
	})
}

// newRoots returns a read-only root and a writable root for the same copy of
// a storage root with the object ark:123/abc.
func newRoots(t *testing.T) (readOnly, writable *ocfl.Root) {
	t.Helper()
	ctx := context.Background()
	_, fixtures := testutil.TempDirTestData(t, "testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root")
	readOnly, err := ocfl.NewRoot(ctx, ocflfs.DirFS(fixtures[0]), ".")
	be.NilErr(t, err)
	fsys, err := local.NewFS(fixtures[0])
	be.NilErr(t, err)
	t.Cleanup(func() { fsys.Close() })
	writable, err = ocfl.NewRoot(ctx, fsys, ".")
	be.NilErr(t, err)
	return readOnly, writable
}

// newHookRoot returns a writable root for a copy of a storage root with the
// object ark:123/abc, and its FS.
func newHookRoot(t *testing.T) (*ocfl.Root, *hookFS) {
	t.Helper()
	_, fixtures := testutil.TempDirTestData(t, "testdata/store-fixtures/1.0/good-stores/reg-extension-dir-root")
	fsys, err := local.NewFS(fixtures[0])
	be.NilErr(t, err)
	t.Cleanup(func() { fsys.Close() })
	hook := &hookFS{FS: fsys}
	root, err := ocfl.NewRoot(context.Background(), hook, ".")
	be.NilErr(t, err)
	return root, hook
}

// hookFS is a local FS that calls beforeOpen, if it's set, before opening a
// file, and beforeWrite, if it's set, before writing one. They may be called
// concurrently.
type hookFS struct {
	*local.FS
	beforeOpen  func()
	beforeWrite func()
}

func (fsys *hookFS) OpenFile(ctx context.Context, name string) (fs.File, error) {
	if fsys.beforeOpen != nil {
		fsys.beforeOpen()
	}
	return fsys.FS.OpenFile(ctx, name)
}

func (fsys *hookFS) Write(ctx context.Context, name string, r io.Reader) (int64, error) {
	if fsys.beforeWrite != nil {
		fsys.beforeWrite()
	}
	return fsys.FS.Write(ctx, name, r)
}

// newFile returns a new stage file for the object id in root.
func newFile(t *testing.T, root *ocfl.Root, id string) *stage.File {
	t.Helper()
	f, err := stage.Create(filepath.Join(t.TempDir(), "stage.json"), newStage(t, root, id))
	be.NilErr(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

// savedStage returns the stage saved in f's stage file.
func savedStage(t *testing.T, f *stage.File) *stage.Stage {
	t.Helper()
	got, err := stage.Open(f.Path())
	be.NilErr(t, err)
	return got.Stage()
}

// lockRemoved checks that f's stage file isn't locked: its lock file doesn't
// exist.
func lockRemoved(t *testing.T, f *stage.File) {
	t.Helper()
	_, err := os.Stat(f.Path() + ".lock")
	be.True(t, errors.Is(err, fs.ErrNotExist))
}

// stageFileRemoved checks that f's stage file doesn't exist.
func stageFileRemoved(t *testing.T, f *stage.File) {
	t.Helper()
	_, err := os.Stat(f.Path())
	be.True(t, errors.Is(err, fs.ErrNotExist))
}
