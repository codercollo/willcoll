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
}

// SecurityAlertData is the template data for SecurityAlertTemplate.
func SecurityAlertData(firmName string, a SecurityAlert) map[string]any {
	return map[string]any{
		"FirmName": firmName,
		"Headline": a.Headline,
		"Actor":    a.Actor,
		"IP":       a.IP,
		"Target":   a.Target,
		"When":     a.At.In(moneyfmt.Nairobi).Format("2 Jan 2006, 15:04 MST"),
	}
}
