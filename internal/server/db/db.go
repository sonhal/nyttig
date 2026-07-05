// Package db provides SQLite database connectivity and schema migrations
// for the Nyttig news aggregator daemon.
//
// The package embeds its own migration SQL files and runs them on Open().
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (or creates) the SQLite database at dsn, runs all pending
// migrations, and returns a connected, migrated *sql.DB.
func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}

	// SQLite serializes writes via a single lock; capping the pool at 1 connection
	// makes writers wait on the Go side rather than hitting SQLITE_BUSY, and WAL
	// keeps reads concurrent.
	db.SetMaxOpenConns(1)

	// Enable WAL mode and foreign keys.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma foreign_keys: %w", err)
	}

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("runMigrations: %w", err)
	}

	return db, nil
}

// runMigrations reads embedded .up.sql files from migrations/ and applies them
// in order, tracking applied migrations in a schema_migrations table.
func runMigrations(db *sql.DB) error {
	// Ensure tracking table exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		dirty   BOOLEAN NOT NULL DEFAULT 0
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Check for dirty state (a previously failed migration).
	var dirty bool
	_ = db.QueryRow("SELECT dirty FROM schema_migrations WHERE dirty = 1 LIMIT 1").Scan(&dirty)
	if dirty {
		return fmt.Errorf("database is in a dirty migration state; manual intervention required")
	}

	// Load applied versions.
	applied := make(map[int]bool)
	rows, err := db.Query("SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("query schema_migrations: %w", err)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("scan version: %w", err)
		}
		applied[v] = true
	}
	rows.Close()

	// List embedded .up.sql files.
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Collect and sort by version.
	type migration struct {
		version int
		name    string
	}
	var migrations []migration
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		// Parse version from "000001_init.up.sql" -> 1.
		parts := strings.SplitN(name, "_", 2)
		v, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("parse migration version from %q: %w", name, err)
		}
		migrations = append(migrations, migration{version: v, name: name})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	// Apply each pending migration in a transaction.
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile("migrations/" + m.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", m.name, err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.version, err)
		}

		// Mark dirty before executing.
		if _, err := tx.Exec("INSERT OR REPLACE INTO schema_migrations (version, dirty) VALUES (?, 1)", m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("mark dirty %d: %w", m.version, err)
		}

		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("execute migration %d (%s): %w", m.version, m.name, err)
		}

		// Mark clean.
		if _, err := tx.Exec("UPDATE schema_migrations SET dirty = 0 WHERE version = ?", m.version); err != nil {
			tx.Rollback()
			return fmt.Errorf("mark clean %d: %w", m.version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}

	return nil
}