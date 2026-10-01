package core

import "testing"

// Version strings come from clients: whatever they send must never panic the comparison.
func FuzzVersions(f *testing.F) {
	for _, s := range []string{"0.3.0", "v1.2.3-dirty", "Windows 10.0 (build 26200)", "", "..", "1..2", "99999999999999999999.1"} {
		f.Add(s, "0.3.0")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		_ = compareVersions(a, b)
		_ = extractVersion(a)
		if compareVersions(a, a) != 0 {
			t.Fatalf("%q must equal itself", a)
		}
		if x, y := compareVersions(a, b), compareVersions(b, a); x != -y {
			t.Fatalf("compare is not antisymmetric for %q, %q: %d %d", a, b, x, y)
		}
	})
}
