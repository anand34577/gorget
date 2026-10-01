//go:build darwin

package posture

func detectDisk() int { return parseFileVault(run("fdesetup", "status")) }

func detectFirewall() int {
	return parseSocketFilter(run("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate"))
}
