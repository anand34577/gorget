package disco

import "testing"

// FuzzOpen: packets from the network are untrusted; garbage must be rejected, never panic.
func FuzzOpen(f *testing.F) {
	var k Key
	f.Add([]byte("short"))
	f.Add(make([]byte, 200))
	f.Fuzz(func(t *testing.T, b []byte) {
		_ = IsDisco(b)
		_, _ = SenderKey(b)
		_, _, _ = Open(b, &k)
	})
}
