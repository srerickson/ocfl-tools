package stage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/stage"
)

func TestID(t *testing.T) {
	// printf 'root:file:///srv/ocfl\nid:ark:123/abc' | sha256sum | xxd -r -p | base32 | tr A-Z a-z | tr -d =
	const want = "zglatzqjlnvy3efmayzehtrdubhqxs3chjx4vfz6mnkmf7757p5q"
	be.Equal(t, want, stage.ID("file:///srv/ocfl", "ark:123/abc"))
	be.Unequal(t, want, stage.ID("file:///srv/ocfl2", "ark:123/abc"))
	be.Unequal(t, want, stage.ID("file:///srv/ocfl", "ark:123/abcd"))
}

func TestCache(t *testing.T) {
	// two IDs that share an eight character prefix
	const (
		id1 = "aaaaaaaab" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		id2 = "aaaaaaaac" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		id3 = "bbbbbbbbb" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		// a stage with a lock file but no stage file
		id4 = "ddddddddd" + "ddddddddddddddddddddddddddddddddddddddddddd"
	)
	dir := t.TempDir()
	cache := stage.Cache{Dir: dir}
	for _, id := range []string{id1, id2, id3} {
		be.NilErr(t, os.WriteFile(cache.Path(id), []byte("{}"), 0o644))
	}
	// files that aren't stage files
	for _, name := range []string{
		"." + id3 + ".json.123",                 // temporary file from Write
		id3 + ".json.lock",                      // lock file
		id4 + ".json.lock",                      // lock file without a stage file
		"cccccccccccccccccccccccccccccccc.json", // too short
		strings.ToUpper(id3) + ".json",
		id3[:51] + "1.json", // not base32
		id3,
	} {
		be.NilErr(t, os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644))
	}
	be.NilErr(t, os.Mkdir(filepath.Join(dir, "c"+id3[1:]+".json"), 0o755))

	t.Run("Path", func(t *testing.T) {
		be.Equal(t, filepath.Join(dir, id1+".json"), cache.Path(id1))
	})

	t.Run("Find", func(t *testing.T) {
		for _, tt := range []struct {
			prefix  string
			want    string
			wantErr string
		}{
			{prefix: "b", want: id3},
			{prefix: id3, want: id3},
			{prefix: "aaaaaaaab", want: id1},
			{prefix: "aaaaaaaa", wantErr: "ambiguous: it matches 2 stages in " + dir + ": " + id1 + ", " + id2},
			{prefix: "c", wantErr: "no stage"},
			{prefix: "B", wantErr: "no stage"},
			{prefix: id3 + "b", wantErr: "no stage"},
			{prefix: "", wantErr: "empty"},
		} {
			got, err := cache.Find(tt.prefix)
			if tt.wantErr != "" {
				be.Nonzero(t, err)
				be.In(t, tt.wantErr, err.Error())
				continue
			}
			be.NilErr(t, err)
			be.Equal(t, tt.want, got)
		}
	})

	t.Run("FindLock", func(t *testing.T) {
		for _, tt := range []struct {
			prefix  string
			want    string
			wantErr string
		}{
			{prefix: "d", want: id4},
			{prefix: "b", want: id3},
			{prefix: "aaaaaaaab", want: id1},
			{prefix: "aaaaaaaa", wantErr: "ambiguous"},
			{prefix: "c", wantErr: "no stage"},
			{prefix: "", wantErr: "empty"},
		} {
			got, err := cache.FindLock(tt.prefix)
			if tt.wantErr != "" {
				be.Nonzero(t, err)
				be.In(t, tt.wantErr, err.Error())
				continue
			}
			be.NilErr(t, err)
			be.Equal(t, tt.want, got)
		}
		// Find doesn't find the stage without a stage file
		_, err := cache.Find("d")
		be.In(t, "no stage", err.Error())
	})

	t.Run("ShortID", func(t *testing.T) {
		for id, want := range map[string]string{
			id1: "aaaaaaaab",
			id2: "aaaaaaaac",
			id3: "bbbbbbb",
			// IDs without stage files
			"aaaaaab" + id1[7:]:  "aaaaaab",
			"aaaaaaaa" + id3[8:]: "aaaaaaaabb",
		} {
			got, err := cache.ShortID(id)
			be.NilErr(t, err)
			be.Equal(t, want, got)
		}
	})

	t.Run("List", func(t *testing.T) {
		got, err := cache.List()
		be.NilErr(t, err)
		be.AllEqual(t, []stage.CacheEntry{
			{ID: id1, ShortID: "aaaaaaaab"},
			{ID: id2, ShortID: "aaaaaaaac"},
			{ID: id3, ShortID: "bbbbbbb"},
		}, got)
	})

	t.Run("List not a directory", func(t *testing.T) {
		notDir := stage.Cache{Dir: cache.Path(id1)}
		_, err := notDir.List()
		be.Nonzero(t, err)
	})

	t.Run("missing directory", func(t *testing.T) {
		missing := stage.Cache{Dir: filepath.Join(dir, "missing")}
		_, err := missing.Find("a")
		be.In(t, "no stage", err.Error())
		short, err := missing.ShortID(id1)
		be.NilErr(t, err)
		be.Equal(t, id1[:7], short)
		entries, err := missing.List()
		be.NilErr(t, err)
		be.Zero(t, len(entries))
	})
}
