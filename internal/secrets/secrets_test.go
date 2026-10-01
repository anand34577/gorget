package secrets

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	b, err := NewBox(RandomBytes(32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Seal("hello")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(s)
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	other, _ := NewBox(RandomBytes(32))
	if _, err := other.Open(s); err == nil {
		t.Fatal("expected failure with wrong key")
	}
	if e, _ := b.Seal(""); e != "" {
		t.Fatal("empty should stay empty")
	}
}

func TestPassword(t *testing.T) {
	h := HashPassword("correct horse")
	if !VerifyPassword(h, "correct horse") {
		t.Fatal("verify failed")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
}
