//go:build !linux

package netfw

// Allow and Revoke only matter on Linux.
func Allow(string)  {}
func Revoke(string) {}
