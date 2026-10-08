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
