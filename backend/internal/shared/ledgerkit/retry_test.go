package ledgerkit

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"wrapped deadlock", fmt.Errorf("posting: %w", &pgconn.PgError{Code: "40P01"}), true},
		{"unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsRetryable(tc.err); got != tc.want {
			t.Errorf("%s: IsRetryable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
