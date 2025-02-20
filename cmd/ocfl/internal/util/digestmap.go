package util

import (
	"iter"
	"maps"
	"slices"

	"github.com/srerickson/ocfl-go"
)

func EachPath(m ocfl.DigestMap) iter.Seq2[string, string] {
	return pathMapEachPath(m.PathMap())
}

func pathMapEachPath(pm ocfl.PathMap) iter.Seq2[string, string] {
	return func(yield func(string, string) bool) {
		paths := slices.Collect(maps.Keys(pm))
		slices.Sort(paths)
		for _, p := range paths {
			if !yield(p, pm[p]) {
				return
			}
		}
	}
}
