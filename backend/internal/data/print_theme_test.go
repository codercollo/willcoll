package data

import (
	"encoding/json"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/validator"
)

func strp(s string) *string { return &s }

func TestResolveTheme(t *testing.T) {
	prop := &Property{Name: "Kiwi Place", Location: "Nairobi"}
	landlord := &Landlord{Phone: "+254700000000"}

	t.Run("no theme falls back to property and landlord", func(t *testing.T) {
		th := ResolveTheme(prop, landlord)
		if th.HeaderText != "Kiwi Place" || th.Phone != "+254700000000" || len(th.AddressLines) != 1 || th.Accent != nil {
			t.Fatalf("unexpected fallback theme: %+v", th)
		}
	})
	t.Run("nil landlord still resolves", func(t *testing.T) {
		if th := ResolveTheme(prop, nil); th.HeaderText != "Kiwi Place" {
			t.Fatalf("got %+v", th)
		}
	})
	t.Run("stored theme overrides", func(t *testing.T) {
		p := *prop
		p.PrintTheme = &PrintTheme{HeaderText: "KIWI PLACE", AddressLines: []string{"P.O. Box 1"}, Phone: "0711", AccentColor: "#1F4E79"}
		th := ResolveTheme(&p, landlord)
		if th.HeaderText != "KIWI PLACE" || th.Phone != "0711" || th.AddressLines[0] != "P.O. Box 1" || th.Accent == nil || th.Accent.R != 0x1F {
			t.Fatalf("got %+v", th)
		}
	})
	t.Run("bad accent is ignored", func(t *testing.T) {
		p := *prop
		p.PrintTheme = &PrintTheme{AccentColor: "navy"}
		if th := ResolveTheme(&p, landlord); th.Accent != nil {
			t.Fatal("invalid accent must fall back to black")
		}
	})
}

func TestResolvePaymentParticulars(t *testing.T) {
	prop := &Property{Name: "Kiwi Place", PayheroChannelID: strp("CH-9")}
	got := ResolvePaymentParticulars(prop, &Landlord{BankName: strp("KCB")})
	if got.AccountName != "Kiwi Place" || got.AccountNumber != "CH-9" || got.BankName != "KCB" {
		t.Fatalf("got %+v", got)
	}
	got = ResolvePaymentParticulars(prop, &Landlord{BankAccountName: strp("Landlord Ltd"), BankAccountNumber: strp("0123")})
	if got.AccountName != "Landlord Ltd" || got.AccountNumber != "0123" {
		t.Fatalf("got %+v", got)
	}
}

func TestDecodePrintTheme(t *testing.T) {
	if decodePrintTheme(nil) != nil {
		t.Error("NULL should decode to nil")
	}
	for _, raw := range []string{"", "null", "{bad"} {
		msg := json.RawMessage(raw)
		if decodePrintTheme(&msg) != nil {
			t.Errorf("%q should decode to nil", raw)
		}
	}
	valid := json.RawMessage(`{"header_text":"X"}`)
	if th := decodePrintTheme(&valid); th == nil || th.HeaderText != "X" {
		t.Fatal("valid theme not decoded")
	}
}

func TestValidatePrintTheme(t *testing.T) {
	v := validator.New()
	ValidatePrintTheme(v, &PrintTheme{AccentColor: "red"})
	if v.Valid() {
		t.Fatal("bad accent should fail")
	}
	v = validator.New()
	ValidatePrintTheme(v, &PrintTheme{AccentColor: "#00ff00", AddressLines: []string{"a"}})
	if !v.Valid() {
		t.Fatalf("valid theme rejected: %v", v.Errors)
	}
}
