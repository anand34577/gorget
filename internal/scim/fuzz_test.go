package scim

import "testing"

// Filters come from identity providers; malformed ones must be rejected, not crash.
func FuzzParseEq(f *testing.F) {
	for _, s := range []string{`userName eq "a@b.c"`, `displayName eq "x"`, ``, `a`, `a eq`, `a ne "b"`, `userName eq "`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { _, _, _ = parseEq(s) })
}
