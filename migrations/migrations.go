// Package migrations owns the schema history.
//
// The migrations are plain SQL files run by golang-migrate, embedded into the
// binary so a deployment never needs the repository. They are written in the
// subset of SQL that PostgreSQL and SQLite both accept (see the header of
// 000001_initial_schema.up.sql), which is why there is one directory rather
// than one per dialect: a single description of the schema, with nothing to
// keep in step.
//
// Rules:
//
//   - a migration that has shipped is never edited, only followed by a new one;
//   - files are named NNNNNN_snake_case.up.sql and .down.sql, in pairs;
//   - every up migration has a down migration that actually reverses it.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database"
	pgxdriver "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	sqlitedriver "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/avarabyeu/tripops-bot/internal/db"
)

//go:embed sql/*.sql
var files embed.FS

// migrationsTable is where golang-migrate records the current version.
const migrationsTable = "schema_migrations"

// Run applies every pending migration.
func Run(ctx context.Context, database *db.DB, log *slog.Logger) error {
	migrator, cleanup, err := newMigrator(database)
	if err != nil {
		return err
	}
	defer cleanup(log)

	before, _, _ := migrator.Version()
	switch err := migrator.Up(); {
	case errors.Is(err, migrate.ErrNoChange):
		if log != nil {
			log.Debug("database schema is up to date", "version", before)
		}
		return nil
	case err != nil:
		return fmt.Errorf("migrations: apply: %w", err)
	}

	after, dirty, err := migrator.Version()
	if err != nil {
		return fmt.Errorf("migrations: read version: %w", err)
	}
	if log != nil {
		log.Info("migrations applied", "from", before, "to", after, "dirty", dirty)
	}
	return nil
}

// Down rolls back the given number of migrations, or all of them when steps is
// zero. It exists for development and for tests; production rollbacks are a
// deliberate, supervised act.
func Down(ctx context.Context, database *db.DB, steps int, log *slog.Logger) error {
	migrator, cleanup, err := newMigrator(database)
	if err != nil {
		return err
	}
	defer cleanup(log)

	if steps <= 0 {
		err = migrator.Down()
	} else {
		err = migrator.Steps(-steps)
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrations: roll back: %w", err)
	}
	return nil
}

// Force marks the schema as being at a version without running anything. It is
// the escape hatch for a migration that failed halfway and left the version
// marked dirty.
func Force(ctx context.Context, database *db.DB, version int, log *slog.Logger) error {
	migrator, cleanup, err := newMigrator(database)
	if err != nil {
		return err
	}
	defer cleanup(log)

	if err := migrator.Force(version); err != nil {
		return fmt.Errorf("migrations: force version %d: %w", version, err)
	}
	return nil
}

// State describes where the database stands.
type State struct {
	Engine  db.Dialect
	Version uint
	// Dirty means a migration failed partway. Nothing else will run until it
	// is resolved, by hand, with Force.
	Dirty   bool
	Applied []string
	Pending []string
}

// Status reports the current version and which files have and have not run.
func Status(database *db.DB) (State, error) {
	state := State{Engine: database.Dialect()}

	migrator, cleanup, err := newMigrator(database)
	if err != nil {
		return state, err
	}
	defer cleanup(nil)

	version, dirty, err := migrator.Version()
	switch {
	case errors.Is(err, migrate.ErrNilVersion):
		version = 0
	case err != nil:
		return state, fmt.Errorf("migrations: read version: %w", err)
	}
	state.Version, state.Dirty = version, dirty

	names, err := Available()
	if err != nil {
		return state, err
	}
	for _, name := range names {
		n, _, ok := strings.Cut(name, "_")
		parsed, convErr := strconv.ParseUint(n, 10, 64)
		if !ok || convErr != nil {
			continue
		}
		if parsed <= uint64(version) {
			state.Applied = append(state.Applied, name)
		} else {
			state.Pending = append(state.Pending, name)
		}
	}
	return state, nil
}

// Available lists the migration names found in the embedded files, in order.
func Available() ([]string, error) {
	entries, err := fs.Glob(files, "sql/*.up.sql")
	if err != nil {
		return nil, fmt.Errorf("migrations: list: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(strings.TrimPrefix(e, "sql/"), ".up.sql"))
	}
	sort.Strings(names)
	return names, nil
}

// newMigrator wires golang-migrate onto the connection the application is
// already using. Reusing the pool matters: an in-memory SQLite database exists
// only inside its own connection, so a second one would migrate a different
// database than the one the tests then query.
func newMigrator(database *db.DB) (*migrate.Migrate, func(*slog.Logger), error) {
	source, err := iofs.New(files, "sql")
	if err != nil {
		return nil, nil, fmt.Errorf("migrations: open embedded files: %w", err)
	}
	closers := []func() error{source.Close}
	cleanup := func(log *slog.Logger) {
		for _, close := range closers {
			if err := close(); err != nil && log != nil {
				log.Warn("closing migration resources", "err", err)
			}
		}
	}

	var driver migratedb.Driver
	switch database.Dialect() {
	case db.Postgres:
		// Migrations get a connection of their own, in simple query mode.
		//
		// pgx defaults to the extended protocol, which refuses a multi-statement
		// Exec; the simple protocol lets PostgreSQL parse the whole file itself.
		// The alternative — golang-migrate's MultiStatementEnabled — splits on
		// every ";" byte without understanding comments or string literals, so a
		// semicolon inside a comment would cut a CREATE TABLE in half.
		//
		// The application pool keeps the extended protocol and its prepared
		// statements; this connection exists only for the migration and is
		// closed with it.
		var conn *sql.DB
		conn, err = openPostgresForMigrations(database.URL())
		if err != nil {
			cleanup(nil)
			return nil, nil, err
		}
		closers = append(closers, conn.Close)
		driver, err = pgxdriver.WithInstance(conn, &pgxdriver.Config{
			MigrationsTable: migrationsTable,
		})
	case db.SQLite:
		// SQLite reuses the application's connection: an in-memory database
		// exists only inside the connection that created it, so a second one
		// would migrate a different database than the caller then queries.
		var conn *sql.DB
		conn, err = database.DB.DB()
		if err != nil {
			cleanup(nil)
			return nil, nil, fmt.Errorf("migrations: unwrap connection: %w", err)
		}
		driver, err = sqlitedriver.WithInstance(conn, &sqlitedriver.Config{
			MigrationsTable: migrationsTable,
		})
	default:
		cleanup(nil)
		return nil, nil, fmt.Errorf("migrations: unsupported engine %q", database.Dialect())
	}
	if err != nil {
		cleanup(nil)
		return nil, nil, fmt.Errorf("migrations: driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", source, string(database.Dialect()), driver)
	if err != nil {
		cleanup(nil)
		return nil, nil, fmt.Errorf("migrations: init: %w", err)
	}
	// Only our own resources are released. migrate.Close would also close the
	// database driver, and for SQLite that is the pool the application is about
	// to serve requests from.
	return migrator, cleanup, nil
}

// openPostgresForMigrations opens a single-connection pool in simple query
// mode, so a migration file runs as one statement batch.
func openPostgresForMigrations(url string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("migrations: parse database url: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	conn := stdlib.OpenDB(*cfg)
	conn.SetMaxOpenConns(1)
	return conn, nil
}
