package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/codercollo/willcoll/backend/internal/shared/ledgerkit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// failingBeginner fails every BeginTx with a fixed Postgres error and counts
// the attempts, so the retry loop can be tested without a database.
type failingBeginner struct {
	code     string
	attempts int
}

func (f *failingBeginner) BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error) {
	f.attempts++
	return nil, &pgconn.PgError{Code: f.code}
}

func TestWithRetryBudget(t *testing.T) {
	cases := []struct {
		name         string
		code         string
		max          int
		wantAttempts int
	}{
		{"serialization failure exhausts the budget", "40001", ledgerkit.MaxSerializationRetries, ledgerkit.MaxSerializationRetries + 1},
		{"deadlock is retried too", "40P01", 2, 3},
		{"other errors are not retried", "23505", ledgerkit.MaxSerializationRetries, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &failingBeginner{code: tc.code}
			s := &SQLStore{}
			err := s.withRetry(context.Background(), b, uuid.New(), tc.max, nil)
			if err == nil {
				t.Fatal("withRetry returned nil, want the Postgres error")
			}
			if b.attempts != tc.wantAttempts {
				t.Errorf("attempts = %d, want %d", b.attempts, tc.wantAttempts)
			}
		})
	}
}
