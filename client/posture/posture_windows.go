//go:build windows

package posture

func detectDisk() int {
	// The system drive; works for BitLocker and "Device encryption" on Home editions.
	return parseBitLocker(run("manage-bde", "-status", "C:"))
}

func detectFirewall() int {
	return parseNetshFirewall(run("netsh", "advfirewall", "show", "allprofiles", "state"))
}
