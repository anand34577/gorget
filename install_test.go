package gorget

import (
	"strings"
	"testing"
)

func TestInstallScript(t *testing.T) {
	s := InstallScript("https://vpn.example.com")
	if !strings.Contains(s, `DEFAULT_SERVER="https://vpn.example.com"`) {
		t.Fatal("the server address should be filled in")
	}
	if !strings.HasPrefix(s, "#!/bin/sh") {
		t.Fatal("the script should start with a shebang")
	}
	if !strings.Contains(InstallPowerShell("https://vpn.example.com"), "$DefaultServer = 'https://vpn.example.com'") {
		t.Fatal("the PowerShell installer should get the server address too")
	}
	for _, bad := range []string{`https://x"; rm -rf /; "`, "https://x`id`", "https://x$(id)"} {
		if strings.Contains(InstallScript(bad), bad) {
			t.Fatalf("unsafe address %q must not be inserted", bad)
		}
	}
}
