package statcache_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-go/digest"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/statcache"
)

// anHourAgo is a modification time that isn't racy.
var anHourAgo = time.Now().Add(-time.Hour)

func TestChecker_Check(t *testing.T) {
	ctx := context.Background()
	checker := &statcache.Checker{Alg: digest.SHA256}

	t.Run("new file", func(t *testing.T) {
		name := writeFile(t, t.TempDir(), "a.txt", "hello", anHourAgo)
		result := checkOne(t, checker, name, nil)
		be.Equal(t, statcache.StatusNew, result.Status)
		be.True(t, result.Digested)
		be.Equal(t, sha256Hex(t, "hello"), result.Entry.Digest)
		be.Equal(t, int64(5), result.Entry.Size)
	})

	t.Run("unchanged file isn't read", func(t *testing.T) {
		name := writeFile(t, t.TempDir(), "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
		be.False(t, result.Digested)
		be.Equal(t, entry, result.Entry)
	})

	t.Run("touched file is unchanged and refreshed", func(t *testing.T) {
		dir := t.TempDir()
		name := writeFile(t, dir, "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		later := anHourAgo.Add(time.Minute)
		be.NilErr(t, os.Chtimes(name, later, later))
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
		be.True(t, result.Digested)
		be.Equal(t, later.UnixNano(), result.Entry.Mtime)
		// the refreshed entry is trusted
		again := checkOne(t, checker, name, &result.Entry)
		be.Equal(t, statcache.StatusUnchanged, again.Status)
		be.False(t, again.Digested)
	})

	t.Run("modified file", func(t *testing.T) {
		dir := t.TempDir()
		name := writeFile(t, dir, "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		writeFile(t, dir, "a.txt", "hello, world", anHourAgo)
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusModified, result.Status)
		be.Equal(t, sha256Hex(t, "hello, world"), result.Entry.Digest)
	})

	t.Run("racy entry with same stat is read", func(t *testing.T) {
		// with minimal comparison, a same-size change with the modification
		// time restored has the same stat. The entry was recorded right after
		// the file was modified, so the stat isn't trusted.
		minimal := &statcache.Checker{Alg: digest.SHA256, Compare: statcache.CompareMinimal}
		dir := t.TempDir()
		mtime := time.Now()
		name := writeFile(t, dir, "a.txt", "hello", mtime)
		entry := checkOne(t, minimal, name, nil).Entry
		writeFile(t, dir, "a.txt", "jello", mtime)
		result := checkOne(t, minimal, name, &entry)
		be.True(t, result.Digested)
		be.Equal(t, statcache.StatusModified, result.Status)
	})

	t.Run("racy window", func(t *testing.T) {
		// the same change isn't found if the file was modified long enough
		// before the entry was recorded: that's the trade-off for not
		// reading files.
		minimal := &statcache.Checker{Alg: digest.SHA256, Compare: statcache.CompareMinimal, RacyWindow: time.Millisecond}
		dir := t.TempDir()
		mtime := time.Now().Add(-time.Second)
		name := writeFile(t, dir, "a.txt", "hello", mtime)
		entry := checkOne(t, minimal, name, nil).Entry
		writeFile(t, dir, "a.txt", "jello", mtime)
		result := checkOne(t, minimal, name, &entry)
		be.False(t, result.Digested)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
	})

	t.Run("missing file", func(t *testing.T) {
		name := writeFile(t, t.TempDir(), "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		be.NilErr(t, os.Remove(name))
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusMissing, result.Status)
		be.Equal(t, statcache.Entry{}, result.Entry)
		be.Equal(t, statcache.StatusMissing, checkOne(t, checker, name, nil).Status)
	})

	t.Run("recreated file", func(t *testing.T) {
		dir := t.TempDir()
		name := writeFile(t, dir, "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		be.NilErr(t, os.Remove(name))
		writeFile(t, dir, "a.txt", "hello", anHourAgo)
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
		be.True(t, result.Digested)
		writeFile(t, dir, "a.txt", "goodbye", anHourAgo)
		be.Equal(t, statcache.StatusModified, checkOne(t, checker, name, &entry).Status)
	})

	t.Run("symlink isn't followed", func(t *testing.T) {
		dir := t.TempDir()
		target := writeFile(t, dir, "a.txt", "hello", anHourAgo)
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks not supported:", err)
		}
		result := checkOne(t, checker, link, nil)
		be.True(t, errors.Is(result.Err, statcache.ErrNotRegular))
		be.Equal(t, statcache.FileStatus(0), result.Status)
	})

	t.Run("directory", func(t *testing.T) {
		result := checkOne(t, checker, t.TempDir(), nil)
		be.True(t, errors.Is(result.Err, statcache.ErrNotRegular))
	})

	t.Run("results are in input order", func(t *testing.T) {
		dir := t.TempDir()
		var items []statcache.Item
		for i := range 100 {
			name := writeFile(t, dir, fmt.Sprintf("%d.txt", i), fmt.Sprint(i), anHourAgo)
			items = append(items, statcache.Item{Path: name})
		}
		workers := &statcache.Checker{Alg: digest.SHA256, Workers: 8}
		results, err := workers.Check(ctx, items)
		be.NilErr(t, err)
		be.Equal(t, len(items), len(results))
		for i, result := range results {
			be.Equal(t, sha256Hex(t, fmt.Sprint(i)), result.Entry.Digest)
		}
	})

	t.Run("no items", func(t *testing.T) {
		results, err := checker.Check(ctx, nil)
		be.NilErr(t, err)
		be.Equal(t, 0, len(results))
	})

	t.Run("canceled", func(t *testing.T) {
		name := writeFile(t, t.TempDir(), "a.txt", "hello", anHourAgo)
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := checker.Check(canceled, []statcache.Item{{Path: name}})
		be.True(t, errors.Is(err, context.Canceled))
	})
}

func TestChecker_Check_compare(t *testing.T) {
	// replaced returns an entry for a file and replaces the file with a new
	// one with the same content, size and modification time, but a
	// different inode and change time.
	replaced := func(t *testing.T, checker *statcache.Checker) (string, statcache.Entry) {
		dir := t.TempDir()
		name := writeFile(t, dir, "a.txt", "hello", anHourAgo)
		entry := checkOne(t, checker, name, nil).Entry
		tmp := writeFile(t, dir, "tmp", "hello", anHourAgo)
		be.NilErr(t, os.Rename(tmp, name))
		return name, entry
	}

	t.Run("all", func(t *testing.T) {
		checker := &statcache.Checker{Alg: digest.SHA256}
		name, entry := replaced(t, checker)
		if after := checkOne(t, checker, name, nil).Entry; after.Stat == entry.Stat {
			t.Skip("inode and change time aren't available on this platform")
		}
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
		be.True(t, result.Digested)
	})

	t.Run("minimal ignores inode and change time", func(t *testing.T) {
		checker := &statcache.Checker{Alg: digest.SHA256, Compare: statcache.CompareMinimal}
		name, entry := replaced(t, checker)
		result := checkOne(t, checker, name, &entry)
		be.Equal(t, statcache.StatusUnchanged, result.Status)
		be.False(t, result.Digested)
	})

	t.Run("minimal compares whole seconds", func(t *testing.T) {
		checker := &statcache.Checker{Alg: digest.SHA256, Compare: statcache.CompareMinimal}
		mtime := anHourAgo.Truncate(time.Second)
		dir := t.TempDir()
		name := writeFile(t, dir, "a.txt", "hello", mtime)
		entry := checkOne(t, checker, name, nil).Entry
		sameSecond := mtime.Add(500 * time.Millisecond)
		be.NilErr(t, os.Chtimes(name, sameSecond, sameSecond))
		be.False(t, checkOne(t, checker, name, &entry).Digested)
		nextSecond := mtime.Add(time.Second)
		be.NilErr(t, os.Chtimes(name, nextSecond, nextSecond))
		be.True(t, checkOne(t, checker, name, &entry).Digested)
	})
}

// BenchmarkChecker_Check compares checking 10,000 files without entries
// (cold), which reads every file, to checking them with entries (warm).
func BenchmarkChecker_Check(b *testing.B) {
	ctx := context.Background()
	dir := b.TempDir()
	content := make([]byte, 16*1024)
	items := make([]statcache.Item, 10_000)
	for i := range items {
		name := filepath.Join(dir, fmt.Sprintf("%05d", i))
		content[0] = byte(i)
		if err := os.WriteFile(name, content, 0o644); err != nil {
			b.Fatal(err)
		}
		if err := os.Chtimes(name, anHourAgo, anHourAgo); err != nil {
			b.Fatal(err)
		}
		items[i] = statcache.Item{Path: name}
	}
	checker := &statcache.Checker{Alg: digest.SHA512}
	b.Run("cold", func(b *testing.B) {
		for b.Loop() {
			if _, err := checker.Check(ctx, items); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("warm", func(b *testing.B) {
		results, err := checker.Check(ctx, items)
		if err != nil {
			b.Fatal(err)
		}
		warm := make([]statcache.Item, len(items))
		for i, result := range results {
			warm[i] = statcache.Item{Path: items[i].Path, Cached: &result.Entry}
		}
		b.ResetTimer()
		for b.Loop() {
			if _, err := checker.Check(ctx, warm); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// checkOne checks the file name with the entry cached, which may be nil.
func checkOne(t *testing.T, checker *statcache.Checker, name string, cached *statcache.Entry) statcache.Result {
	t.Helper()
	results, err := checker.Check(context.Background(), []statcache.Item{{Path: name, Cached: cached}})
	be.NilErr(t, err)
	return results[0]
}

// writeFile writes data to the file name in dir, sets its modification time,
// and returns its path.
func writeFile(t *testing.T, dir, name, data string, mtime time.Time) string {
	t.Helper()
	full := filepath.Join(dir, name)
	be.NilErr(t, os.WriteFile(full, []byte(data), 0o644))
	be.NilErr(t, os.Chtimes(full, mtime, mtime))
	return full
}

// sha256Hex returns the sha256 digest of data, from the digest package rather
// than from statcache.
func sha256Hex(t *testing.T, data string) string {
	t.Helper()
	d := digest.SHA256.Digester()
	_, err := d.Write([]byte(data))
	be.NilErr(t, err)
	return d.String()
}
