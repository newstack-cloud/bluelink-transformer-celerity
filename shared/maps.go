package shared

import (
	"maps"
	"slices"
)

// SortedKeys returns the keys of the given map sorted in lexicographic order.
func SortedKeys[T any](m map[string]T) []string {
	return slices.Sorted(maps.Keys(m))
}
