package stage

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// shortIDLen is the minimum length of the stage ID prefixes returned by
// [Cache.ShortID].
const shortIDLen = 7

// idAlphabet is the characters in stage IDs.
const idAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// idEncoding is the encoding of stage IDs: lowercase, unpadded base32.
var idEncoding = base32.NewEncoding(idAlphabet).WithPadding(base32.NoPadding)

// idLen is the length of a stage ID: an encoded SHA-256 digest.
var idLen = idEncoding.EncodedLen(sha256.Size)

// ID returns the stage ID for the object id in the storage root at the location
// rootLoc. It is the SHA-256 digest of the root location and object ID,
// encoded with lowercase, unpadded base32.
func ID(rootLoc, id string) string {
	sum := sha256.Sum256([]byte("root:" + rootLoc + "\nid:" + id))
	return idEncoding.EncodeToString(sum[:])
}

// Cache is a directory of stage files named by their stage IDs, so there is at
// most one stage for each object in a storage root.
type Cache struct {
	Dir string
}

// Path returns the path of the stage file for the stage ID id.
func (c Cache) Path(id string) string {
	return filepath.Join(c.Dir, id+".json")
}

// Create saves s in the new stage file for the stage ID id, creating c's
// directory if needed. Like the function [Create], it doesn't replace an
// existing stage file.
func (c Cache) Create(id string, s *Stage) (*File, error) {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating stage cache: %w", err)
	}
	return Create(c.Path(id), s)
}

// Find returns the ID of the stage file in c whose ID starts with prefix. It
// returns an error if there isn't exactly one.
func (c Cache) Find(prefix string) (string, error) {
	ids, err := c.ids()
	if err != nil {
		return "", err
	}
	return c.find(prefix, ids)
}

// FindLock is like [Cache.Find], but it also finds stages that have a lock
// file but no stage file, such as one left behind by a command that was
// killed before it created the stage file, so that the lock can be removed
// with [RemoveLock].
func (c Cache) FindLock(prefix string) (string, error) {
	ids, err := c.ids()
	if err != nil {
		return "", err
	}
	locks, err := c.lockIDs()
	if err != nil {
		return "", err
	}
	for _, id := range locks {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return c.find(prefix, ids)
}

// ShortID returns the shortest prefix of the stage ID id, with at least seven
// characters, that doesn't start the ID of any other stage file in c.
func (c Cache) ShortID(id string) (string, error) {
	ids, err := c.ids()
	if err != nil {
		return "", err
	}
	return shortID(id, ids), nil
}

// CacheEntry is a stage file in a [Cache], as listed by [Cache.List].
type CacheEntry struct {
	// ID is the stage ID. The stage file is [Cache.Path](ID).
	ID string

	// ShortID is the shortest prefix of ID, with at least seven
	// characters, that doesn't start the ID of any other stage file in the
	// listing. It's what [Cache.ShortID] returns for ID, and what [Cache.Find]
	// resolves back to ID, if the cache hasn't changed since it was listed.
	ShortID string
}

// List returns the stage files in c, sorted by stage ID. Like [Cache.Find],
// it ignores files that aren't stage files, such as temporary files from
// [File.Save] and lock files. The stage files aren't opened, so List doesn't
// report whether they can be read: use [Open] with [Cache.Path]. If c's
// directory doesn't exist, there are no stage files, and List returns an
// empty slice and no error. It returns an error only if the directory can't be
// read.
func (c Cache) List() ([]CacheEntry, error) {
	ids, err := c.ids()
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	entries := make([]CacheEntry, len(ids))
	for i, id := range ids {
		entries[i] = CacheEntry{ID: id, ShortID: shortID(id, ids)}
	}
	return entries, nil
}

// ids returns the IDs of the stage files in c. Other files, such as temporary
// files from [File.Save] and lock files, are ignored. If c's directory doesn't
// exist, there are no stage files.
func (c Cache) ids() ([]string, error) {
	return c.idsWithSuffix(".json")
}

// idsWithSuffix returns the stage IDs of the regular files in c named by a
// stage ID followed by suffix. If c's directory doesn't exist, there are none.
func (c Cache) idsWithSuffix(suffix string) ([]string, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading stage cache: %w", err)
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), suffix)
		if ok && e.Type().IsRegular() && validID(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// lockIDs returns the IDs of the stages in c that have a lock file.
func (c Cache) lockIDs() ([]string, error) {
	return c.idsWithSuffix(lockPath(".json"))
}

// find returns the ID in ids that starts with prefix. It returns an error if
// there isn't exactly one.
func (c Cache) find(prefix string, ids []string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty stage ID")
	}
	var found []string
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no stage with ID %q in %s", prefix, c.Dir)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("stage ID %q is ambiguous: it matches %d stages in %s: %s",
			prefix, len(found), c.Dir, strings.Join(found, ", "))
	}
}

// shortID returns the shortest prefix of id, with at least seven characters,
// that doesn't start any of ids other than id itself.
func shortID(id string, ids []string) string {
	n := min(shortIDLen, len(id))
	for _, other := range ids {
		if other == id {
			continue
		}
		common := 0
		for common < len(id) && common < len(other) && id[common] == other[common] {
			common++
		}
		n = max(n, min(common+1, len(id)))
	}
	return id[:n]
}

// validID returns true if id has the length and characters of a stage ID.
func validID(id string) bool {
	return len(id) == idLen && strings.Trim(id, idAlphabet) == ""
}
