package posture

import "testing"

func TestParsers(t *testing.T) {
	if parseBitLocker("Protection Status:    Protection On") != Yes || parseBitLocker("Conversion Status: Fully Decrypted") != No || parseBitLocker("ERROR") != Unknown {
		t.Fatal("bitlocker")
	}
	on := "Domain Profile Settings:\nState                                 ON\nPrivate Profile Settings:\nState                                 ON\n"
	if parseNetshFirewall(on) != Yes || parseNetshFirewall(on+"State   OFF\n") != No || parseNetshFirewall("") != Unknown {
		t.Fatal("netsh")
	}
	if parseFileVault("FileVault is On.") != Yes || parseFileVault("FileVault is Off.") != No {
		t.Fatal("filevault")
	}
	if parseSocketFilter("Firewall is enabled. (State = 1)") != Yes || parseSocketFilter("Firewall is disabled. (State = 0)") != No {
		t.Fatal("socketfilter")
	}
}

func TestDetectDoesNotPanic(t *testing.T) {
	s := Detect()
	t.Logf("disk=%d firewall=%d", s.DiskEncrypted, s.Firewall)
}
