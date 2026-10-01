package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSessionsAndSamples(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, "sqlite", filepath.Join(t.TempDir(), "gorget.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := &Device{ID: "d1", Name: "laptop", Kind: KindNative, WGPublicKey: "k1", IPv4: "100.80.0.2", IPv6: "fd00::2", State: StateActive, Tags: StringList{}, Endpoints: StringList{}, CustomAllowedIPs: StringList{}, CreatedAt: Now()}
	if err := st.CreateDevice(ctx, d); err != nil {
		t.Fatal(err)
	}
	s1 := &DeviceSession{ID: "s1", DeviceID: "d1", StartedAt: 100, PublicIP: "203.0.113.1", Country: "DE"}
	s2 := &DeviceSession{ID: "s2", DeviceID: "d1", StartedAt: 200, PublicIP: "198.51.100.1", Country: "IN"}
	for _, s := range []*DeviceSession{s1, s2} {
		if err := st.OpenSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CloseSession(ctx, "s1", 150); err != nil {
		t.Fatal(err)
	}
	got, err := st.DeviceSessions(ctx, "d1", 10)
	if err != nil || len(got) != 2 || got[0].ID != "s2" || got[1].EndedAt != 150 {
		t.Fatalf("sessions: %+v %v", got, err)
	}
	cs, err := st.KnownCountries(ctx, "d1", 0)
	if err != nil || len(cs) != 2 {
		t.Fatalf("countries: %v %v", cs, err)
	}
	if err := st.CloseOpenSessions(ctx, 300); err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{1000, 2000, 3000} {
		if err := st.InsertSample(ctx, &StatsSample{TS: ts, Total: 3, Online: 2, Rx: 10, Tx: 5}); err != nil {
			t.Fatal(err)
		}
	}
	xs, err := st.Samples(ctx, 2000)
	if err != nil || len(xs) != 2 || xs[0].TS != 2000 {
		t.Fatalf("samples: %+v %v", xs, err)
	}
	if err := st.PruneHistory(ctx, 2500, 1000); err != nil {
		t.Fatal(err)
	}
	if xs, _ := st.Samples(ctx, 0); len(xs) != 1 {
		t.Fatalf("pruned samples: %+v", xs)
	}
	if got, _ := st.DeviceSessions(ctx, "d1", 10); len(got) != 0 {
		t.Fatalf("finished sessions older than the cut-off should be gone: %+v", got)
	}
}
