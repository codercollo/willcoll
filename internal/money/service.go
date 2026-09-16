package money

import "github.com/jackc/pgx/v5/pgxpool"

// Service owns every write to the ledger. Each Execute*Tx method lives on this
// Service, never as a bare package function.
type Service struct {
	pool *pgxpool.Pool
}

// NewService constructs a money Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}
