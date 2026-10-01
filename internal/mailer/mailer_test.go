package mailer

import (
	"strings"
	"testing"
)

func TestBuildRejectsHeaderInjection(t *testing.T) {
	c := Config{Host: "smtp.example.com", Port: 587, Security: SecuritySTARTTLS, From: "vpn@example.com"}
	if _, err := build(c, []string{"a@example.com"}, Message{Subject: "hi\r\nBcc: x@evil.test", Text: "x"}); err == nil {
		t.Fatal("a subject with a line break must be rejected")
	}
	b, err := build(c, []string{"a@example.com"}, Message{Subject: "Grüße", Text: "hello", HTML: "<p>hello</p>"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"From: <vpn@example.com>", "To: a@example.com", "=?utf-8?q?Gr=C3=BC=C3=9Fe?=", "text/plain", "text/html"} {
		if !strings.Contains(s, want) {
			t.Errorf("message lacks %q:\n%s", want, s)
		}
	}
}

func TestValidate(t *testing.T) {
	good := Config{Host: "smtp.example.com", Port: 465, Security: SecurityTLS, From: "Gorget <vpn@example.com>"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Config{
		{Host: "", Port: 465, Security: SecurityTLS, From: "vpn@example.com"},
		{Host: "smtp.example.com", Port: 0, Security: SecurityTLS, From: "vpn@example.com"},
		{Host: "smtp.example.com", Port: 465, Security: "ssl3", From: "vpn@example.com"},
		{Host: "smtp.example.com", Port: 465, Security: SecurityTLS, From: "not an address"},
	} {
		if bad.Validate() == nil {
			t.Errorf("expected %+v to be invalid", bad)
		}
	}
}
