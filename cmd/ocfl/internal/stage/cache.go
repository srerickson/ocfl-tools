package stage

import (
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// WriteNew writes s to the stage file for the stage ID id, creating c's
// directory if needed. Like the function [WriteNew], it doesn't replace an
// existing stage file.
func (c Cache) WriteNew(id string, s *Stage) error {
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("creating stage cache: %w", err)
	}
	return WriteNew(c.Path(id), s)
}

// Find returns the ID of the stage file in c whose ID starts with prefix. It
// returns an error if there isn't exactly one.
func (c Cache) Find(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("empty stage ID")
	}
	ids, err := c.ids()
	if err != nil {
		return "", err
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

// ShortID returns the shortest prefix of the stage ID id, with at least seven
// characters, that doesn't start the ID of any other stage file in c.
func (c Cache) ShortID(id string) (string, error) {
	ids, err := c.ids()
	if err != nil {
		return "", err
	}
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
	return id[:n], nil
}

// ids returns the IDs of the stage files in c. Other files, such as temporary
// files from [Write], are ignored. If c's directory doesn't exist, there are
// no stage files.
func (c Cache) ids() ([]string, error) {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading stage cache: %w", err)
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if ok && e.Type().IsRegular() && validID(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// validID returns true if id has the length and characters of a stage ID.
func validID(id string) bool {
	return len(id) == idLen && strings.Trim(id, idAlphabet) == ""
}
