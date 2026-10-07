// Package dbhealth reads a Postgres database's size, connection count, largest
// tables and WAL-archiver state, for the Super Admin's system health page.
// Product-agnostic: it takes any *sql.DB, so platform-api can check
// willcoll_platform with it and each product API can check its own database.
package dbhealth

import (
	"context"
	"database/sql"
	"time"
)

// Snapshot is one database's health. The nil time/string fields mean the WAL
// archiver has never run or never failed. The archiver is per cluster, not per
// database, so every database on one server reports the same archiver state.
type Snapshot struct {
	SizeBytes   int64
	Connections int

	WALArchivedCount int64
	LastArchivedWAL  *string
	LastArchivedTime *time.Time
	WALFailedCount   int64
	LastFailedTime   *time.Time

	// LargestTables is the top five tables by total size (table + indexes +
	// TOAST): where the database's disk use is going.
	LargestTables []TableSize
}

// TableSize is one table's total on-disk size.
type TableSize struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// Check reads the snapshot. It is hand-written SQL rather than sqlc because
// sqlc has no types for the pg_stat_* views. The caller sets the deadline.
func Check(ctx context.Context, db *sql.DB) (Snapshot, error) {
	const query = `
		SELECT pg_database_size(current_database()),
		       (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
		       a.archived_count, a.last_archived_wal, a.last_archived_time,
		       a.failed_count, a.last_failed_time
		FROM pg_stat_archiver a`

	var s Snapshot
	err := db.QueryRowContext(ctx, query).Scan(
		&s.SizeBytes, &s.Connections,
		&s.WALArchivedCount, &s.LastArchivedWAL, &s.LastArchivedTime,
		&s.WALFailedCount, &s.LastFailedTime,
	)
	if err != nil {
		return s, err
	}

	const tablesQuery = `
		SELECT c.relname, pg_total_relation_size(c.oid)
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relkind = 'r'
		ORDER BY pg_total_relation_size(c.oid) DESC
		LIMIT 5`

	rows, err := db.QueryContext(ctx, tablesQuery)
	if err != nil {
		return s, err
	}
	defer func() { _ = rows.Close() }() // rows.Err() is checked below

	for rows.Next() {
		var t TableSize
		if err := rows.Scan(&t.Name, &t.SizeBytes); err != nil {
			return s, err
		}
		s.LargestTables = append(s.LargestTables, t)
	}
	return s, rows.Err()
}
