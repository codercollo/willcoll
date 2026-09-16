package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
)

const requestTxContextKey contextKey = "requestTx"

// tenantScope runs immediately after authenticate. It opens the request's DB
// transaction, scopes it to the caller's organization_id, and stores the
// transaction on the request context so handlers can use it.
func (s *Server) tenantScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromContext(r.Context())
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "invalid or missing authentication token")
			return
		}

		tx, err := s.pool.Begin(r.Context())
		if err != nil {
			s.logger.Error("begin request transaction", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}
		defer func() { _ = tx.Rollback(r.Context()) }()

		if err := s.tenancy.Scope(r.Context(), tx, claims.OrganizationID); err != nil {
			s.logger.Error("scope request transaction", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "the server encountered a problem and could not process your request")
			return
		}

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), requestTxContextKey, tx)))

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		if status >= http.StatusBadRequest {
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			s.logger.Error("commit request transaction", "error", err)
		}
	})
}

// requestTxFromContext returns the scoped transaction opened by tenantScope.
func requestTxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(requestTxContextKey).(pgx.Tx)
	return tx, ok
}
