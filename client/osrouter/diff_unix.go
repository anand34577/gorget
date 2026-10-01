//go:build linux || darwin

package osrouter

import (
	"net/netip"
	"slices"
)

func diffPrefixes(old, cur []netip.Prefix) (add, del []netip.Prefix) {
	for _, p := range cur {
		if !slices.Contains(old, p) {
			add = append(add, p)
		}
	}
	for _, p := range old {
		if !slices.Contains(cur, p) {
			del = append(del, p)
		}
	}
	return add, del
}
