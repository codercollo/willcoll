// Command migrate is the one-shot entrypoint behind `make migrate`. It loads
// config, then applies every migration in internal/db/migrations.
package main

import (
	"log"

	"github.com/codercollo/willcoll-sys/internal/config"
	"github.com/codercollo/willcoll-sys/internal/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := db.RunMigrations(cfg.DatabaseDSN); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	log.Println("migrations applied")
}
