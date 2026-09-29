package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/codercollo/willcoll/backend/internal/db/sqlc"
	"github.com/codercollo/willcoll/backend/internal/pdf"
	"github.com/codercollo/willcoll/backend/internal/validator"
)

// PrintTheme is the properties.print_theme document: how a property's
// tenant-facing PDFs are headed and accented. Every field is optional. There
// is no logo: PDFs are rendered on demand and Willcoll stores no blobs.
type PrintTheme struct {
	HeaderText   string   `json:"header_text,omitempty"`
	AddressLines []string `json:"address_lines,omitempty"`
	Phone        string   `json:"phone,omitempty"`
	// AccentColor is "#RRGGBB"; it colours only the header rule and title.
	AccentColor string `json:"accent_color,omitempty"`
	// ReconnectionNote overrides pdf.DefaultReconnectionNote on bills.
	ReconnectionNote string `json:"reconnection_note,omitempty"`
}

var accentRX = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func ValidatePrintTheme(v *validator.Validator, t *PrintTheme) {
	v.Check(len(t.HeaderText) <= 200, "header_text", "must not be more than 200 bytes long")
	v.Check(len(t.AddressLines) <= 4, "address_lines", "must not have more than 4 lines")
	for _, l := range t.AddressLines {
		v.Check(len(l) <= 200, "address_lines", "each line must not be more than 200 bytes long")
	}
	v.Check(len(t.Phone) <= 50, "phone", "must not be more than 50 bytes long")
	v.Check(t.AccentColor == "" || accentRX.MatchString(t.AccentColor), "accent_color", "must be a #RRGGBB colour")
	v.Check(len(t.ReconnectionNote) <= 500, "reconnection_note", "must not be more than 500 bytes long")
}

// decodePrintTheme reads the jsonb column. A NULL or unreadable document
// yields nil, so a bad theme can never stop a document rendering.
func decodePrintTheme(raw *json.RawMessage) *PrintTheme {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil
	}
	var t PrintTheme
	if err := json.Unmarshal(*raw, &t); err != nil {
		return nil
	}
	return &t
}

// SetPrintTheme saves (or with a nil theme, clears) the property's print
// theme, bumping Version. Returns ErrEditConflict if the version moved on.
func (m PropertyModel) SetPrintTheme(ctx context.Context, property *Property, theme *PrintTheme) error {
	ctx, cancel := withTimeout(ctx, m.Timeout)
	defer cancel()

	var raw *json.RawMessage
	if theme != nil {
		b, err := json.Marshal(theme)
		if err != nil {
			return err
		}
		msg := json.RawMessage(b)
		raw = &msg
	}

	return m.Store.ExecTenantTx(ctx, property.TenantID, func(q sqlc.Querier) error {
		version, err := q.SetPropertyPrintTheme(ctx, sqlc.SetPropertyPrintThemeParams{
			TenantID: property.TenantID, ID: property.ID, Version: property.Version, PrintTheme: raw,
		})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrEditConflict
			}
			return err
		}
		property.Version = version
		property.PrintTheme = theme
		return nil
	})
}

// ResolveTheme builds the pdf.Theme for a property's documents: the stored
// print theme where set, else the property's name and location and the
// landlord's phone, else plain black-on-white. It never fails, so a
// document is never blocked for lack of a theme.
func ResolveTheme(property *Property, landlord *Landlord) pdf.Theme {
	th := pdf.Theme{HeaderText: property.Name}
	if property.Location != "" {
		th.AddressLines = []string{property.Location}
	}
	if landlord != nil {
		th.Phone = landlord.Phone
	}

	pt := property.PrintTheme
	if pt == nil {
		return th
	}
	if s := strings.TrimSpace(pt.HeaderText); s != "" {
		th.HeaderText = s
	}
	if len(pt.AddressLines) > 0 {
		th.AddressLines = pt.AddressLines
	}
	if s := strings.TrimSpace(pt.Phone); s != "" {
		th.Phone = s
	}
	if pt.AccentColor != "" {
		if c, err := pdf.ParseHexColor(pt.AccentColor); err == nil {
			th.Accent = c
		}
	}
	th.ReconnectionNote = pt.ReconnectionNote
	return th
}

// ResolvePaymentParticulars is the "pay to" block on water and garbage
// bills: the property as account name, the landlord's bank details, with the
// PayHero channel as the account number when the landlord has none.
func ResolvePaymentParticulars(property *Property, landlord *Landlord) pdf.PaymentParticulars {
	p := pdf.PaymentParticulars{AccountName: property.Name}
	if landlord != nil {
		if landlord.BankAccountName != nil && *landlord.BankAccountName != "" {
			p.AccountName = *landlord.BankAccountName
		}
		if landlord.BankAccountNumber != nil {
			p.AccountNumber = *landlord.BankAccountNumber
		}
		if landlord.BankName != nil {
			p.BankName = *landlord.BankName
		}
	}
	if p.AccountNumber == "" && property.PayheroChannelID != nil {
		p.AccountNumber = *property.PayheroChannelID
	}
	return p
}

// reconnectionNoteFor composes the bill's reconnection-fee line from the
// property's structured fee when one is configured — never a placeholder
// when it isn't. A manager's free-text override (print_theme) still wins
// when no structured fee is set, for backward compatibility.
func reconnectionNoteFor(property *Property, th pdf.Theme) string {
	if property.ReconnectionFee != nil {
		return fmt.Sprintf("NB: If disconnected you will be required to pay a reconnection fee of Ksh. %s/=", property.ReconnectionFee.Display())
	}
	return th.ReconnectionNote
}
