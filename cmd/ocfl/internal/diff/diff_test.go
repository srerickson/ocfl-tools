package diff_test

import (
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/diff"
)

func TestDiff(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		paths := map[string]string{"a.txt": "1", "b.txt": "2"}
		result, err := diff.Diff(paths, paths)
		be.NilErr(t, err)
		be.True(t, result.Empty())
		be.Equal(t, "", result.String())
	})
	t.Run("added, removed, and modified", func(t *testing.T) {
		a := map[string]string{"keep.txt": "1", "mod.txt": "2", "rm.txt": "3"}
		b := map[string]string{"keep.txt": "1", "mod.txt": "4", "new.txt": "5"}
		result, err := diff.Diff(a, b)
		be.NilErr(t, err)
		be.False(t, result.Empty())
		be.AllEqual(t, []string{"new.txt"}, result.Added)
		be.AllEqual(t, []string{"rm.txt"}, result.Removed)
		be.AllEqual(t, []string{"mod.txt"}, result.Modified)
		be.Zero(t, len(result.Renamed))
	})
	t.Run("renamed", func(t *testing.T) {
		a := map[string]string{"old.txt": "1"}
		b := map[string]string{"new.txt": "1"}
		result, err := diff.Diff(a, b)
		be.NilErr(t, err)
		be.Equal(t, "new.txt", result.Renamed["old.txt"])
		be.Zero(t, len(result.Added))
		be.Zero(t, len(result.Removed))
	})
	t.Run("copies beyond renames are added", func(t *testing.T) {
		a := map[string]string{"old.txt": "1"}
		b := map[string]string{"x.txt": "1", "y.txt": "1"}
		result, err := diff.Diff(a, b)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Renamed))
		be.Equal(t, 1, len(result.Added))
	})
	t.Run("removals beyond renames are removed", func(t *testing.T) {
		a := map[string]string{"x.txt": "1", "y.txt": "1"}
		b := map[string]string{"new.txt": "1"}
		result, err := diff.Diff(a, b)
		be.NilErr(t, err)
		be.Equal(t, 1, len(result.Renamed))
		be.Equal(t, 1, len(result.Removed))
	})
}
