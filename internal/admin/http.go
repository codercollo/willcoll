package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "willcoll_admin_session"
const sessionTTL = 2 * time.Hour

// Server is the whole admin panel's HTTP surface — its own mux, its own
// session store, never mounted alongside internal/api's routes/middleware
// (spec 23.3). Single hardcoded operator: no user table, no signup, no
// password reset (spec 23.1).
type Server struct {
	service      *Service
	username     string
	passwordHash string // bcrypt
	logger       *slog.Logger
	sessionsMu   sync.Mutex
	sessions     map[string]time.Time // token -> expiry
}

func NewServer(service *Service, username, passwordHash string, logger *slog.Logger) *Server {
	return &Server{
		service:      service,
		username:     username,
		passwordHash: passwordHash,
		logger:       logger,
		sessions:     make(map[string]time.Time),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", s.handleLoginPage)
	mux.HandleFunc("POST /admin/login", s.handleLogin)
	mux.HandleFunc("POST /admin/logout", s.requireAuth(s.handleLogout))
	mux.HandleFunc("GET /admin/", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("GET /admin/organizations", s.requireAuth(s.handleOrganizations))
	mux.HandleFunc("POST /admin/organizations/{id}", s.requireAuth(s.handleUpdateOrganization))
	mux.HandleFunc("GET /admin/addons", s.requireAuth(s.handleAddons))
	mux.HandleFunc("GET /admin/audit-log", s.requireAuth(s.handleAuditLog))
	mux.HandleFunc("GET /admin/operations", s.requireAuth(s.handleOperations))
	return mux
}

// --- auth: one hardcoded credential, one in-memory session map (spec 23.1).
// This is intentionally not a distributed session store — the admin panel
// is a single hardcoded operator, single process; a restart simply logs
// them out.

func (s *Server) newSessionToken() string {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	s.sessionsMu.Lock()
	s.sessions[token] = time.Now().Add(sessionTTL)
	s.sessionsMu.Unlock()
	return token
}

func (s *Server) validSession(token string) bool {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	expiry, ok := s.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(s.sessions, token)
		return false
	}
	return true
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !s.validSession(cookie.Value) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func constantTimeEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	renderPageWithNav(w, "Admin Login", loginTmpl, nil, false)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	password := r.FormValue("password")

	if !constantTimeEqual(username, s.username) ||
		bcrypt.CompareHashAndPassword([]byte(s.passwordHash), []byte(password)) != nil {
		renderPageWithNav(w, "Admin Login", loginTmpl, map[string]any{"Error": "invalid credentials"}, false)
		return
	}

	token := s.newSessionToken()
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(sessionTTL),
	})
	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessionsMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/admin", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// --- screens (spec 23.4-23.6 only — see package doc for the non-goals)

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "Willcoll Platform Admin", dashboardTmpl, map[string]any{})
}

func (s *Server) handleOrganizations(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.service.ListOrganizations(r.Context())
	if err != nil {
		s.logger.Error("list organizations", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	renderPage(w, "Organizations", organizationsTmpl, map[string]any{"Orgs": orgs})
}

func (s *Server) handleUpdateOrganization(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid organization id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if err := s.service.UpdateOrganization(
		r.Context(), id,
		strings.TrimSpace(r.FormValue("subscription_tier")),
		strings.TrimSpace(r.FormValue("billing_status")),
		s.username,
	); err != nil {
		s.logger.Error("update organization", "error", err)
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/admin/organizations", http.StatusSeeOther)
}

func (s *Server) handleAddons(w http.ResponseWriter, r *http.Request) {
	subs, err := s.service.ListScoreAddonSubscribers(r.Context())
	if err != nil {
		s.logger.Error("list addon subscribers", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	renderPage(w, "Verified Property Score Subscribers", addonsTmpl, map[string]any{"Subs": subs})
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	entries, err := s.service.ListAuditLog(r.Context(), 200)
	if err != nil {
		s.logger.Error("list audit log", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	renderPage(w, "Audit Log", auditLogTmpl, map[string]any{"Entries": entries})
}

// stuckPaymentThreshold flags a gateway transaction stuck PENDING for over
// an hour — long enough that IntaSend's own webhook retry window has
// passed, short enough an operator can still act same-day.
const stuckPaymentThreshold = time.Hour

func (s *Server) handleOperations(w http.ResponseWriter, r *http.Request) {
	drift, err := s.service.LedgerDrift(r.Context())
	if err != nil {
		s.logger.Error("ledger drift check", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	stuck, err := s.service.ListStuckGatewayTransactions(r.Context(), stuckPaymentThreshold)
	if err != nil {
		s.logger.Error("list stuck payments", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	renderPage(w, "Operations", operationsTmpl, map[string]any{
		"Drift": drift.DriftAccounts,
		"Stuck": stuck,
	})
}

// --- rendering: plain html/template, no framework, matches this codebase's
// hand-rolled-everything convention elsewhere (LGF style).

func renderPage(w http.ResponseWriter, title, body string, data any) {
	renderPageWithNav(w, title, body, data, true)
}

func renderPageWithNav(w http.ResponseWriter, title, body string, data any, showNav bool) {
	tmpl := template.Must(template.New("page").Parse(layoutTmpl))
	tmpl = template.Must(tmpl.New("body").Parse(body))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "page", map[string]any{"Title": title, "Data": data, "ShowNav": showNav}); err != nil {
		http.Error(w, fmt.Sprintf("render error: %v", err), http.StatusInternalServerError)
	}
}
