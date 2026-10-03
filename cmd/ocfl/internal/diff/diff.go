package diff

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	addStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	remStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	modStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	movStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

// Result describes the changes between two sets of paths. Renamed maps
// removed paths to added paths with the same digest.
type Result struct {
	Added    []string
	Removed  []string
	Modified []string
	Renamed  map[string]string
}

// Diff compares aPaths and bPaths, which map file paths to digests, and returns
// the changes from a to b.
func Diff(aPaths, bPaths map[string]string) (result Result, err error) {
	addMap := map[string][]string{} // digest map of new files in b
	rmMap := map[string][]string{}  // digest map of missing files in b
	for aPath, aDigest := range aPaths {
		bDigest, inB := bPaths[aPath]
		switch {
		case !inB:
			// aPath is not in bPaths: it was removed
			rmMap[aDigest] = append(rmMap[aDigest], aPath)
		case inB && bDigest != aDigest:
			// modified
			result.Modified = append(result.Modified, aPath)
		}
	}
	for bPath, bDigest := range bPaths {
		if _, inA := aPaths[bPath]; !inA {
			// bPath is not in aPaths: it's new
			addMap[bDigest] = append(addMap[bDigest], bPath)
		}
	}
	// build renames by finding matching digests in added / removed
	renamed := map[string]string{}
	for dig, addPaths := range addMap {
		rmPaths := rmMap[dig]
		slices.Sort(addPaths) // sort to make result deterministic
		slices.Sort(rmPaths)
		switch {
		case len(addPaths) > len(rmPaths):
			// create a rename pair for each rmPath
			for i, rmPath := range rmPaths {
				renamed[rmPath] = addPaths[i]
			}
			// remaining paths are added
			result.Added = append(result.Added, addPaths[len(rmPaths):]...)
		default:
			// len(addPaths) <= len(rmPaths)
			// create a rename pair for each addPath
			for i, addPath := range addPaths {
				renamed[rmPaths[i]] = addPath
			}
			// remaining are removed
			result.Removed = append(result.Removed, rmPaths[len(addPaths):]...)
		}
	}
	for dig, rmPaths := range rmMap {
		if _, ok := addMap[dig]; ok {
			continue
		}
		result.Removed = append(result.Removed, rmPaths...)
	}
	if len(renamed) > 0 {
		result.Renamed = renamed
	}
	slices.Sort(result.Added)
	slices.Sort(result.Removed)
	slices.Sort(result.Modified)
	return
}

// String returns the changes in r, one per line, for display in a terminal.
func (r Result) String() string {
	b := &strings.Builder{}
	for _, n := range r.Added {
		fmt.Fprintln(b, addStyle.Render("add:"), n)
	}
	for _, n := range r.Removed {
		fmt.Fprintln(b, remStyle.Render("rem:"), n)
	}
	for _, n := range r.Modified {
		fmt.Fprintln(b, modStyle.Render("mod:"), n)
	}
	for _, n := range slices.Sorted(maps.Keys(r.Renamed)) {
		fmt.Fprintln(b, movStyle.Render("mov:"), "{", n, "=>", r.Renamed[n], "}")
	}
	return b.String()
}

// Empty reports whether r has no changes.
func (r Result) Empty() bool {
	return len(r.Added) == 0 &&
		len(r.Removed) == 0 &&
		len(r.Modified) == 0 &&
		len(r.Renamed) == 0
}
