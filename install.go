// Package gorget holds files that live at the top of the repository but are also
// built into the binaries.
package gorget

import (
	_ "embed"
	"strings"
)

//go:embed install.sh
var installScript string

//go:embed install.ps1
var installPS1 string

// safeServer reports whether a server address can be placed inside a quoted shell or
// PowerShell string without being able to break out of it.
func safeServer(s string) bool {
	return s != "" && !strings.ContainsAny(s, "\"'`$\\\n\r ;&|<>(){}")
}

// InstallScript returns the Linux/macOS client installer with server filled in as
// the default server to connect to ("" leaves it empty).
func InstallScript(server string) string {
	if !safeServer(server) {
		return installScript
	}
	return strings.Replace(installScript, `DEFAULT_SERVER=""`, `DEFAULT_SERVER="`+server+`"`, 1)
}

// InstallPowerShell returns the Windows client installer with server filled in.
func InstallPowerShell(server string) string {
	if !safeServer(server) {
		return installPS1
	}
	return strings.Replace(installPS1, `$DefaultServer = ''`, `$DefaultServer = '`+server+`'`, 1)
}
