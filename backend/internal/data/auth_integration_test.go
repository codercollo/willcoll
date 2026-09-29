package data

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/codercollo/willcoll/backend/internal/db"
	"github.com/google/uuid"
)

// TestAuthIntegration runs the registration / activation SQL (Greenlight
// ch.14) against real Postgres as willcoll_app.
func TestAuthIntegration(t *testing.T) {
	conn := openTestDB(t)
	models := NewModelsFromStore(db.NewStore(conn), integrationTimeout)
	ctx := context.Background()

	email := "Office-" + uuid.NewString()[:8] + "@Example.com"
	manager := &Manager{
		FirmName: "Runda Estates", Username: "u-" + uuid.NewString()[:8], Email: email,
		Phone: "+254712345678", Status: ManagerStatusPending,
	}
	if err := manager.Password.Set("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if err := models.Managers.Insert(ctx, manager); err != nil {
		t.Fatalf("Managers.Insert: %v", err)
	}
	t.Cleanup(func() { conn.Exec(`DELETE FROM managers WHERE id = $1`, manager.ID) })

	// citext: the email matches case-insensitively, and duplicates are
	// rejected case-insensitively too.
	if got, err := models.Managers.GetByEmail(ctx, strings.ToLower(email)); err != nil || got.ID != manager.ID {
		t.Errorf("GetByEmail(lowercase) = %v, %v", got, err)
	}
	dup := *manager
	dup.Username = "u-" + uuid.NewString()[:8]
	dup.Email = strings.ToUpper(email)
	if err := models.Managers.Insert(ctx, &dup); !errors.Is(err, ErrDuplicateEmail) {
		t.Errorf("duplicate email in other case: err = %v, want ErrDuplicateEmail", err)
	}

	activation, err := models.Tokens.New(ctx, manager.ID, 3*24*time.Hour, ScopeActivation)
	if err != nil {
		t.Fatalf("Tokens.New: %v", err)
	}
	reset, err := models.Tokens.New(ctx, manager.ID, time.Hour, ScopePasswordReset)
	if err != nil {
		t.Fatalf("Tokens.New(reset): %v", err)
	}
	if len(activation.Plaintext) != 26 {
		t.Errorf("token plaintext is %d chars, want 26", len(activation.Plaintext))
	}

	// Lookup by plaintext, within the right scope only.
	if got, err := models.Managers.GetForToken(ctx, ScopeActivation, activation.Plaintext); err != nil || got.ID != manager.ID {
		t.Errorf("GetForToken(activation) = %v, %v", got, err)
	}
	if _, err := models.Managers.GetForToken(ctx, ScopePasswordReset, activation.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("activation token accepted as a reset token: err = %v", err)
	}

	// Expired tokens don't resolve.
	expired, err := models.Tokens.New(ctx, manager.ID, -time.Minute, ScopeActivation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.Managers.GetForToken(ctx, ScopeActivation, expired.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expired token resolved: err = %v", err)
	}

	// Resending after expiry (POST /v1/tokens/activation): a fresh token
	// resolves, and the expired one stays dead.
	resent, err := models.Tokens.New(ctx, manager.ID, 3*24*time.Hour, ScopeActivation)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := models.Managers.GetForToken(ctx, ScopeActivation, resent.Plaintext); err != nil || got.ID != manager.ID {
		t.Errorf("resent activation token did not resolve: %v", err)
	}
	if _, err := models.Managers.GetForToken(ctx, ScopeActivation, expired.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("the expired token came back to life after a resend: err = %v", err)
	}

	// Activate, then burn the activation tokens only.
	now := time.Now()
	manager.Status, manager.ActivatedAt = ManagerStatusActive, &now
	if err := models.Managers.Update(ctx, manager); err != nil || manager.Version != 2 {
		t.Fatalf("Managers.Update: err = %v, version = %d", err, manager.Version)
	}
	if err := models.Tokens.DeleteAllForManager(ctx, ScopeActivation, manager.ID); err != nil {
		t.Fatalf("DeleteAllForManager: %v", err)
	}
	if _, err := models.Managers.GetForToken(ctx, ScopeActivation, activation.Plaintext); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("activation token still valid after activation: err = %v", err)
	}
	if _, err := models.Managers.GetForToken(ctx, ScopePasswordReset, reset.Plaintext); err != nil {
		t.Errorf("deleting activation tokens removed the reset token: %v", err)
	}

	got, err := models.Managers.Get(ctx, manager.ID)
	if err != nil || got.Status != ManagerStatusActive || got.ActivatedAt == nil {
		t.Fatalf("Managers.Get after activation = %+v, %v", got, err)
	}
	if ok, err := got.Password.Matches("correct horse battery"); err != nil || !ok {
		t.Errorf("stored password doesn't match: %v, %v", ok, err)
	}
}
