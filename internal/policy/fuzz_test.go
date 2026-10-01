package policy

import "testing"

// FuzzParse: malformed policy documents must produce errors, never panics.
func FuzzParse(f *testing.F) {
	f.Add(`{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`)
	f.Add(`{ // comment
	"groups": {"group:a": ["a@b.c"]},}`)
	f.Add(`{`)
	f.Fuzz(func(t *testing.T, doc string) {
		if p, err := Parse(doc); err == nil && p == nil {
			t.Fatal("nil policy without error")
		}
	})
}

func FuzzParsePorts(f *testing.F) {
	f.Add("22,80,8000-9000")
	f.Add("*")
	f.Fuzz(func(t *testing.T, s string) { _, _ = ParsePorts(s) })
}
