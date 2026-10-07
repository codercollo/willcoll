package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errBadPaySession = errors.New("invalid or expired pay session")

// paySession is what a verified one-time code entitles the payer to: paying
// for one unit, as one phone, for a short while. It names the tenant so the
// intent-status poll (whose URL has no property slug) knows whose data to read.
type paySession struct {
	TenantID uuid.UUID `json:"t"`
	UnitID   uuid.UUID `json:"u"`
	Phone    string    `json:"p"`
	Expires  int64     `json:"e"`
}

func (app *application) paySecret() []byte { return []byte(app.config.pay.sessionSecret) }

// paySessionsEnabled reports whether the public pay endpoints can work: they
// are keyed on a secret and stay off without one.
func (app *application) paySessionsEnabled() bool { return app.config.pay.sessionSecret != "" }

func macOf(secret []byte, purpose, msg string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(purpose))
	h.Write([]byte{0})
	h.Write([]byte(msg))
	return h.Sum(nil)
}

// signPaySession returns a token "<payload>.<signature>" that expires at exp.
func (app *application) signPaySession(s paySession, exp time.Time) (string, error) {
	s.Expires = exp.Unix()
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	sig := base64.RawURLEncoding.EncodeToString(macOf(app.paySecret(), "pay-session", payload))
	return payload + "." + sig, nil
}

// verifyPaySession checks a token's signature and expiry.
func (app *application) verifyPaySession(token string, now time.Time) (*paySession, error) {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok || payload == "" || sig == "" || !app.paySessionsEnabled() {
		return nil, errBadPaySession
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || subtle.ConstantTimeCompare(got, macOf(app.paySecret(), "pay-session", payload)) != 1 {
		return nil, errBadPaySession
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, errBadPaySession
	}
	var s paySession
	if err := json.Unmarshal(raw, &s); err != nil || now.Unix() >= s.Expires {
		return nil, errBadPaySession
	}
	return &s, nil
}

// otpHash is the keyed hash stored in place of a one-time code. It binds the
// code to its unit and phone, so a hash cannot be replayed elsewhere.
func (app *application) otpHash(unitID uuid.UUID, phone, code string) string {
	return hex.EncodeToString(macOf(app.paySecret(), "pay-otp", unitID.String()+"|"+phone+"|"+code))
}
