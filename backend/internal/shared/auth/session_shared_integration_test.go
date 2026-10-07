package auth

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/codercollo/willcoll/backend/internal/testenv"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestSessionSharedAcrossHostsIntegration is system-design.txt E.5's "session
// shared across app hosts, logout signs out everywhere": one login, made by
// one binary's session manager, is honoured by another binary's manager over
// the same willcoll_platform sessions table (the shared cookie name is the
// only other thing they need to agree on), and one logout revokes it for
// both because it is a single row delete.
func TestSessionSharedAcrossHostsIntegration(t *testing.T) {
	dsns := testenv.Require(t, "WILLCOLL_TEST_PLATFORM_DB_DSN")
	db, err := sql.Open("pgx", dsns[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}

	cfg := SessionConfig{CookieName: "willcoll_session", Lifetime: time.Hour, IdleTimeout: time.Hour}
	platformAPI := NewSessionManager(cfg, db) // creates and destroys
	rentalsAPI := NewSessionManager(cfg, db)  // an independent instance, e.g. rentals-api's reader
	const key = "principal_id"

	mux := func(sm *scs.SessionManager) http.Handler {
		h := http.NewServeMux()
		h.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
			sm.Put(r.Context(), key, "manager-1")
		})
		h.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(sm.GetString(r.Context(), key)))
		})
		h.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
			if err := sm.Destroy(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		})
		return sm.LoadAndSave(h)
	}
	platformH, rentalsH := mux(platformAPI), mux(rentalsAPI)

	do := func(h http.Handler, path string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if c != nil {
			req.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	login := do(platformH, "/login", nil)
	var cookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == cfg.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login set no session cookie")
	}

	if got := do(rentalsH, "/whoami", cookie).Body.String(); got != "manager-1" {
		t.Fatalf("the other host saw principal %q, want %q", got, "manager-1")
	}

	if w := do(platformH, "/logout", cookie); w.Code != http.StatusOK {
		t.Fatalf("logout status = %d", w.Code)
	}
	if got := do(rentalsH, "/whoami", cookie).Body.String(); got != "" {
		t.Errorf("after logout the other host still saw principal %q", got)
	}
	if got := do(platformH, "/whoami", cookie).Body.String(); got != "" {
		t.Errorf("after logout the logging-out host still saw principal %q", got)
	}
}
