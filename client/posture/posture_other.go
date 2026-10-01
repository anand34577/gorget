//go:build !windows && !darwin && !linux

package posture

func detectDisk() int     { return Unknown }
func detectFirewall() int { return Unknown }
