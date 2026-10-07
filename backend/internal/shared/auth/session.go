// Package auth holds the product-agnostic authentication primitives every
// product's API binary builds on: SCS session-manager wiring, and the
// random-token generation/hashing scheme behind Greenlight-style
// activation/password-reset tokens. The tokens table itself, and anything
// that resolves a token or session to a domain principal (a manager, a
// staff member), stays in each product's own package — this is only the
// primitive layer.
package auth

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
)

// SessionConfig configures NewSessionManager.
type SessionConfig struct {
	// CookieName is the session cookie's name, e.g. "willcoll_session".
	CookieName  string
	Lifetime    time.Duration
	IdleTimeout time.Duration
	// Secure sets the cookie's Secure flag; false only in local development.
	Secure bool
}

// NewSessionManager configures SCS (ADR 0001): server-side sessions stored
// in the caller's Postgres sessions table, so revoking one is a row
// delete. The cookie is HttpOnly, SameSite=Lax, and Secure per cfg.
func NewSessionManager(cfg SessionConfig, db *sql.DB) *scs.SessionManager {
	sm := scs.New()
	sm.Store = postgresstore.New(db)
	sm.Lifetime = cfg.Lifetime
	sm.IdleTimeout = cfg.IdleTimeout
	sm.Cookie.Name = cfg.CookieName
	sm.Cookie.HttpOnly = true
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Secure = cfg.Secure
	sm.Cookie.Persist = true
	return sm
}
