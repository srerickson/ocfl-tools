// Package statcache detects changes to files without reading them when it can,
// the way git does for 'git status'. A file's metadata ([Stat]) and digest are
// recorded in an [Entry]. Later, the file is checked by comparing its current
// metadata with the entry's: if it matches, the file is unchanged and isn't
// read. Otherwise, the file is digested, and it has changed only if its digest
// differs.
//
// A matching stat isn't trusted for "racily clean" entries: files modified
// shortly before their stat was recorded, within the timestamp resolution of
// the file system. A file changed again within the same timestamp tick, with
// the same size, would have the same stat, so racy entries are always digested.
// See git's Documentation/technical/racy-git.txt.
//
// Entries are values: callers keep them (for example, in a saved file) and
// pass them back to [Checker.Check].
package statcache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/srerickson/ocfl-go/digest"
)

// DefaultRacyWindow is the default for [Checker.RacyWindow]: a file modified
// less than this long before its stat was recorded is digested even if its
// stat matches. It covers file systems with timestamps in whole seconds or
// two-second units (FAT), and some clock skew between a client and a
// network file system.
const DefaultRacyWindow = 2 * time.Second

// ErrNotRegular is returned for a path that isn't a regular file, including a
// symbolic link: links aren't followed.
var ErrNotRegular = errors.New("not a regular file")

// Stat is the file metadata compared to detect changes. Fields that aren't
// available on a platform are zero, so they always match there.
type Stat struct {
	Size  int64       `json:"size"`
	Mtime int64       `json:"mtime"` // modification time, ns since the Unix epoch
	Mode  fs.FileMode `json:"mode"`
	Ctime int64       `json:"ctime,omitempty"` // inode change time, ns since the Unix epoch
	Ino   uint64      `json:"ino,omitempty"`
	Dev   uint64      `json:"dev,omitempty"`
	UID   uint32      `json:"uid,omitempty"`
	GID   uint32      `json:"gid,omitempty"`
}

// StatOf returns the Stat for info, which should be from [os.Lstat].
func StatOf(info fs.FileInfo) Stat {
	st := Stat{
		Size:  info.Size(),
		Mtime: info.ModTime().UnixNano(),
		Mode:  info.Mode(),
	}
	sysStat(info.Sys(), &st)
	return st
}

// Entry is the recorded state of a file.
type Entry struct {
	Stat
	Digest string `json:"digest"`
	// Checked is when Stat was read, in ns since the Unix epoch. It is used to
	// detect racily clean entries.
	Checked int64 `json:"checked"`
}

// CompareMode sets which [Stat] fields are compared. It is like git's
// core.checkStat setting.
type CompareMode int

const (
	// CompareAll compares every field available on the platform.
	CompareAll CompareMode = iota
	// CompareMinimal compares only the size, the modification time in whole
	// seconds, and the mode. Use it for network file systems (SMB/CIFS, NFS)
	// where the inode, device, owner and change time aren't stable, so that
	// unchanged files aren't digested on every check.
	CompareMinimal
)

// FileStatus is the result of checking a file.
type FileStatus int

const (
	// StatusNew is the status of a file checked without an entry.
	StatusNew FileStatus = iota + 1
	// StatusUnchanged is the status of a file with the entry's digest.
	StatusUnchanged
	// StatusModified is the status of a file with a different digest than
	// the entry's.
	StatusModified
	// StatusMissing is the status of a file that doesn't exist.
	StatusMissing
)

func (s FileStatus) String() string {
	switch s {
	case StatusNew:
		return "new"
	case StatusUnchanged:
		return "unchanged"
	case StatusModified:
		return "modified"
	case StatusMissing:
		return "missing"
	default:
		return fmt.Sprintf("FileStatus(%d)", int(s))
	}
}

// Item is a file to check.
type Item struct {
	Path   string // file path for the OS
	Cached *Entry // the file's recorded entry, or nil if it has none
}

// Result is the result of checking an [Item].
type Result struct {
	// Status is zero if Err isn't nil.
	Status FileStatus
	// Entry is the file's current entry: the item's cached entry if the file
	// wasn't digested, or a new entry if it was. For a [StatusUnchanged] file
	// that was digested, the new entry has a fresh stat, so saving it lets
	// the next check skip digesting the file. Entry is zero if the file is
	// [StatusMissing] or Err isn't nil.
	Entry Entry
	// Digested is true if the file was read to digest it.
	Digested bool
	// Err is an error checking the file, such as a permission error or an
	// error wrapping [ErrNotRegular].
	Err error
}

// Checker checks files for changes. Its zero value is not usable: Alg must be
// set.
type Checker struct {
	// Alg is the digest algorithm for entries' digests. It is required.
	Alg digest.Algorithm
	// Compare sets which stat fields are compared.
	Compare CompareMode
	// RacyWindow is how long after a file's modification time its stat is
	// trusted. If it is zero, DefaultRacyWindow is used.
	RacyWindow time.Duration
	// Workers is the number of files checked concurrently. If it is less than
	// 1, runtime.GOMAXPROCS(0) is used. On network file systems, each stat is
	// a round trip, so more workers than CPUs can help.
	Workers int
}

// Check checks the files in items and returns their results in the same
// order. Each item is checked independently. Problems with a file are
// reported in its Result's Err: Check only returns an error if ctx is
// canceled.
func (c *Checker) Check(ctx context.Context, items []Item) ([]Result, error) {
	if c.Alg == nil {
		panic("statcache: Checker.Alg must not be nil")
	}
	workers := c.Workers
	if workers < 1 {
		workers = runtime.GOMAXPROCS(0)
	}
	workers = min(workers, len(items))
	results := make([]Result, len(items))
	next := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range next {
				results[i] = c.check(ctx, items[i])
			}
		})
	}
feed:
	for i := range items {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// check checks a single item.
func (c *Checker) check(ctx context.Context, item Item) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: err}
	}
	// the time is taken before the stat: a file modified after it was
	// recorded but in the same timestamp tick is racy.
	checked := time.Now().UnixNano()
	info, err := os.Lstat(item.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return Result{Status: StatusMissing}
	}
	if err != nil {
		return Result{Err: err}
	}
	if !info.Mode().IsRegular() {
		return Result{Err: &fs.PathError{Op: "lstat", Path: item.Path, Err: ErrNotRegular}}
	}
	st := StatOf(info)
	if item.Cached != nil && c.clean(item.Cached, st) {
		return Result{Status: StatusUnchanged, Entry: *item.Cached}
	}
	// the stat is read before the file is digested, so a change while the
	// file is read is found by the next check.
	dig, err := c.digest(ctx, item.Path)
	if err != nil {
		return Result{Err: err}
	}
	result := Result{
		Entry:    Entry{Stat: st, Digest: dig, Checked: checked},
		Digested: true,
	}
	switch {
	case item.Cached == nil:
		result.Status = StatusNew
	case item.Cached.Digest == dig:
		result.Status = StatusUnchanged
	default:
		result.Status = StatusModified
	}
	return result
}

// clean returns true if a file with the stat st can be trusted to have the
// entry's content without reading it.
func (c *Checker) clean(entry *Entry, st Stat) bool {
	if !c.statEqual(entry.Stat, st) {
		return false
	}
	window := c.RacyWindow
	if window == 0 {
		window = DefaultRacyWindow
	}
	return entry.Mtime < entry.Checked-window.Nanoseconds()
}

// statEqual returns true if a and b match in the fields set by c.Compare.
func (c *Checker) statEqual(a, b Stat) bool {
	if c.Compare == CompareMinimal {
		const sec = int64(time.Second)
		return a.Size == b.Size && a.Mode == b.Mode && a.Mtime/sec == b.Mtime/sec
	}
	return a == b
}

// digest returns the digest of the file name.
func (c *Checker) digest(ctx context.Context, name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	digester := c.Alg.Digester()
	if _, err := io.Copy(digester, ctxReader{ctx: ctx, r: f}); err != nil {
		return "", fmt.Errorf("digesting %s: %w", name, err)
	}
	return digester.String(), nil
}

// ctxReader is an io.Reader that stops reading when ctx is canceled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (r ctxReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
