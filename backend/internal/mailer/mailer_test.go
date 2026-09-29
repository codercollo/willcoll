package mailer

import (
	"strings"
	"testing"
)

func TestRenderTemplates(t *testing.T) {
	tests := []struct {
		file        string
		data        map[string]any
		wantSubject string
		mustContain []string
	}{
		{
			file: "manager_welcome.tmpl",
			data: map[string]any{
				"FirmName": "Runda & Sons <Estates>", "Username": "runda",
				"ActivationURL":   "http://localhost:3000/auth/activate?token=ABCDEFGHIJKLMNOPQRSTUVWXYZ",
				"ActivationToken": "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ExpiresIn": "3 days",
			},
			wantSubject: "Welcome to Willcoll — activate your account",
			mustContain: []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "3 days", "/auth/activate?token="},
		},
		{
			file: "password_reset.tmpl",
			data: map[string]any{
				"FirmName":   "Runda & Sons <Estates>",
				"ResetURL":   "http://localhost:3000/auth/reset-password?token=ABCDEFGHIJKLMNOPQRSTUVWXYZ",
				"ResetToken": "ABCDEFGHIJKLMNOPQRSTUVWXYZ", "ExpiresIn": "45 minutes",
			},
			wantSubject: "Reset your Willcoll password",
			mustContain: []string{"ABCDEFGHIJKLMNOPQRSTUVWXYZ", "45 minutes", "/auth/reset-password?token="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			msg, err := Render(tt.file, tt.data)
			if err != nil {
				t.Fatal(err)
			}

			if msg.Subject != tt.wantSubject {
				t.Errorf("subject = %q, want %q", msg.Subject, tt.wantSubject)
			}
			for _, s := range tt.mustContain {
				if !strings.Contains(msg.PlainBody, s) || !strings.Contains(msg.HTMLBody, s) {
					t.Errorf("both parts should contain %q", s)
				}
			}

			// Plain text is not HTML-escaped; the HTML part is.
			if !strings.Contains(msg.PlainBody, "Hi Runda & Sons <Estates>,") {
				t.Errorf("plain body should carry the firm name verbatim:\n%s", msg.PlainBody)
			}
			if strings.Contains(msg.HTMLBody, "<Estates>") || !strings.Contains(msg.HTMLBody, "Runda &amp; Sons &lt;Estates&gt;") {
				t.Error("HTML body must escape the firm name")
			}

			// Branded with the design tokens: navy header, blue button, canvas.
			for _, color := range []string{"#0B1220", "#2F6FED", "#F1F3F6"} {
				if !strings.Contains(msg.HTMLBody, color) {
					t.Errorf("HTML body missing design-token colour %s", color)
				}
			}
		})
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	if _, err := Render("nope.tmpl", nil); err == nil {
		t.Error("expected an error for a missing template")
	}
}
