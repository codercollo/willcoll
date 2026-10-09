package mailer

import (
	"strings"
	"testing"
	"time"
)

func TestSecurityAlertRenders(t *testing.T) {
	// 21:30 UTC is 00:30 the next day in Nairobi: the email must show Nairobi.
	at := time.Date(2026, 10, 8, 21, 30, 0, 0, time.UTC)
	data := SecurityAlertData("Kimani & Associates", SecurityAlert{
		Headline: "A ledger entry was reversed",
		Actor:    "Wanjiru <accountant>",
		IP:       "41.90.1.2",
		Target:   "Reversed KES 12,000.00 (entry abc)",
		At:       at,
	})

	msg, err := Render(SecurityAlertTemplate, data)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if msg.Subject != "Willcoll alert: A ledger entry was reversed" {
		t.Errorf("subject = %q", msg.Subject)
	}
	for _, want := range []string{"Kimani & Associates", "41.90.1.2", "KES 12,000.00", "9 Oct 2026, 00:30 EAT", "Wanjiru <accountant>"} {
		if !strings.Contains(msg.PlainBody, want) {
			t.Errorf("plain body missing %q:\n%s", want, msg.PlainBody)
		}
	}
	// The HTML body escapes what the actor typed.
	if strings.Contains(msg.HTMLBody, "<accountant>") || !strings.Contains(msg.HTMLBody, "&lt;accountant&gt;") {
		t.Errorf("HTML body does not escape the actor name:\n%s", msg.HTMLBody)
	}
}

func TestSecurityAlertShowsTheRecordLinkOnlyWhenThereIsOne(t *testing.T) {
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	with, err := Render(SecurityAlertTemplate, SecurityAlertData("Firm", SecurityAlert{Headline: "H", Actor: "A", IP: "1.2.3.4", Target: "T", At: at, Link: "https://rentals.example.com/properties/p1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with.PlainBody, "https://rentals.example.com/properties/p1") || !strings.Contains(with.HTMLBody, `href="https://rentals.example.com/properties/p1"`) {
		t.Errorf("link missing:\n%s\n%s", with.PlainBody, with.HTMLBody)
	}
	without, err := Render(SecurityAlertTemplate, SecurityAlertData("Firm", SecurityAlert{Headline: "H", Actor: "A", IP: "1.2.3.4", Target: "T", At: at}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without.PlainBody, "Record:") || strings.Contains(without.HTMLBody, "View the record") {
		t.Errorf("an empty link still rendered a line:\n%s", without.PlainBody)
	}
}

func TestMaskPhone(t *testing.T) {
	for in, want := range map[string]string{
		"+254722000123":    "+254••••••123",
		"+254 722 000 123": "+254••••••123",
		"0722000123":       "072••••123",
		"12345":            "••••",
		"":                 "••••",
	} {
		if got := MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
