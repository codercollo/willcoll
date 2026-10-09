package mailer

import (
	"time"

	"github.com/codercollo/willcoll/backend/pkg/moneyfmt"
)

// SecurityAlertTemplate is the template every owner alert is sent with
// (docs/security/authorization.md, D5).
const SecurityAlertTemplate = "security_alert.tmpl"

// SecurityAlert is one owner alert: who did what, from where, and when.
type SecurityAlert struct {
	// Headline names the event, e.g. "A payment channel was changed". It is the
	// email subject and heading.
	Headline string
	// Actor is who did it: a staff member's name and role, or the firm login.
	Actor string
	// IP is the client address of the request.
	IP string
	// Target is what it was done to, including the amount for a reversal.
	Target string
	// At is when it happened; shown in Africa/Nairobi, the business calendar.
	At time.Time
	// Link is where the owner can see the record (an absolute URL on the
	// product's frontend). Empty hides the line.
	Link string
}

// SecurityAlertData is the template data for SecurityAlertTemplate. Every
// value is a plain string, so the map survives a round trip through the
// email outbox's jsonb payload unchanged.
func SecurityAlertData(firmName string, a SecurityAlert) map[string]any {
	return map[string]any{
		"FirmName": firmName,
		"Headline": a.Headline,
		"Actor":    a.Actor,
		"IP":       a.IP,
		"Target":   a.Target,
		"When":     a.At.In(moneyfmt.Nairobi).Format("2 Jan 2006, 15:04 MST"),
		"Link":     a.Link,
	}
}

// MaskPhone keeps the first and last three digits of a phone number and masks
// the middle ("+254722000123" -> "+254••••••123"), for any alert that has to
// refer to a person by phone. Alerts carry no secrets and no full phone
// numbers.
func MaskPhone(phone string) string {
	digits := 0
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits <= 6 {
		return "••••"
	}
	var b []rune
	seen := 0
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			seen++
			if seen > 3 && seen <= digits-3 {
				b = append(b, '•')
				continue
			}
		} else if r == ' ' || r == '-' {
			continue
		}
		b = append(b, r)
	}
	return string(b)
}
