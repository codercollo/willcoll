package tenancy

import (
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service is the multi-tenancy package's entry point. It holds the DB pool and
// exposes the operations that create and scope Organizations.
type Service struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewService constructs a tenancy Service backed by pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:   pool,
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}
