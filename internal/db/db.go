// Package db owns the database connection and the dialect differences.
//
// TripOps runs on PostgreSQL in production and on SQLite everywhere else —
// local development, CI, and the integration tests, which is why they need no
// infrastructure at all. GORM is what makes that practical: every repository
// in this codebase is written once and runs on both engines.
//
// The rules that keep it that way:
//
//   - no dialect-specific SQL in repositories (no FILTER, no array types, no
//     unnest, no advisory locks, no jsonb operators);
//   - timestamps are Go time.Time in UTC, never database now();
//   - ids and dates are text columns (see core.ID and core.Date);
//   - upserts go through clause.OnConflict, which both drivers implement.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	// The pure Go SQLite driver, registered as "sqlite". It is the only
	// SQLite driver linked into the binary on purpose: golang-migrate's
	// sqlite driver uses the same one, and a second implementation
	// registering the same name would panic at start-up.
	_ "modernc.org/sqlite"
)

// Dialect is the engine behind a connection.
type Dialect string

const (
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

// DB is the application's handle on the database.
type DB struct {
	*gorm.DB
	dialect Dialect
	url     string
}

// Options configure a connection.
type Options struct {
	// URL is either a PostgreSQL DSN (postgres://…) or a SQLite target
	// (sqlite://file.db, file:…, :memory:, or a bare path ending in .db).
	URL      string
	MaxConns int
	// SlowQueryThreshold logs statements that take longer; zero disables it.
	SlowQueryThreshold time.Duration
	Log                *slog.Logger
	Debug              bool
}

// Open connects, verifies the connection and applies pool settings.
func Open(ctx context.Context, opts Options) (*DB, error) {
	dialect := DialectOf(opts.URL)

	var dialector gorm.Dialector
	switch dialect {
	case Postgres:
		dialector = postgres.Open(opts.URL)
	case SQLite:
		// Opened by hand rather than through the dialector's DSN so the
		// connection uses the pure Go driver, not the cgo one the GORM SQLite
		// dialector defaults to.
		conn, err := sql.Open("sqlite", SQLiteDSN(opts.URL))
		if err != nil {
			return nil, fmt.Errorf("db: open sqlite: %w", err)
		}
		dialector = sqlite.Dialector{Conn: conn}
	default:
		return nil, fmt.Errorf("db: cannot tell which engine %q is for", opts.URL)
	}

	gormCfg := &gorm.Config{
		// Ask GORM to normalise driver errors, so repositories can check for a
		// duplicate key without knowing which engine produced it.
		TranslateError: true,
		// Timestamps are the domain's business: services set them explicitly so
		// that the same value is used in the row and in the notification.
		NowFunc: func() time.Time { return time.Now().UTC() },
		Logger:  newLogger(opts),
	}

	gdb, err := gorm.Open(dialector, gormCfg)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", dialect, err)
	}
	if opts.Debug {
		gdb = gdb.Debug()
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("db: pool: %w", err)
	}
	switch dialect {
	case Postgres:
		if opts.MaxConns > 0 {
			sqlDB.SetMaxOpenConns(opts.MaxConns)
			sqlDB.SetMaxIdleConns(max(2, opts.MaxConns/2))
		}
		sqlDB.SetConnMaxLifetime(time.Hour)
		sqlDB.SetConnMaxIdleTime(15 * time.Minute)
	case SQLite:
		// SQLite takes a database-wide write lock. More than one writer buys
		// nothing but "database is locked" errors.
		sqlDB.SetMaxOpenConns(1)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return &DB{DB: gdb, dialect: dialect, url: opts.URL}, nil
}

// Dialect reports which engine is in use, for the rare place that must care.
func (d *DB) Dialect() Dialect { return d.dialect }

// URL is the connection string this handle was opened with. The migration
// runner needs it to open a connection of its own.
func (d *DB) URL() string { return d.url }

// Ping checks the connection, for the readiness probe.
func (d *DB) Ping(ctx context.Context) error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close releases the pool.
func (d *DB) Close() error {
	sqlDB, err := d.DB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// InTx runs fn inside a transaction, rolling back on error or panic.
func (d *DB) InTx(ctx context.Context, fn func(tx *gorm.DB) error) error {
	return d.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(tx)
	})
}

// DialectOf guesses the engine from a connection string.
func DialectOf(url string) Dialect {
	lower := strings.ToLower(strings.TrimSpace(url))
	switch {
	case strings.HasPrefix(lower, "postgres://"), strings.HasPrefix(lower, "postgresql://"),
		strings.Contains(lower, "host=") && strings.Contains(lower, "dbname="):
		return Postgres
	case strings.HasPrefix(lower, "sqlite://"), strings.HasPrefix(lower, "sqlite3://"),
		strings.HasPrefix(lower, "file:"), lower == ":memory:",
		strings.HasSuffix(lower, ".db"), strings.HasSuffix(lower, ".sqlite"),
		strings.HasSuffix(lower, ".sqlite3"):
		return SQLite
	default:
		return ""
	}
}

// SQLiteDSN turns a configured URL into what the driver expects, and applies
// the two pragmas the schema depends on.
//
// Foreign keys are off by default in SQLite and the schema relies on them, so
// enabling them is not optional. busy_timeout turns the "database is locked"
// error that a concurrent writer would otherwise get into a short wait.
func SQLiteDSN(url string) string {
	dsn := url
	for _, prefix := range []string{"sqlite://", "sqlite3://"} {
		if rest, ok := strings.CutPrefix(url, prefix); ok {
			dsn = rest
			break
		}
	}
	for param, value := range map[string]string{
		"_pragma=foreign_keys": "_pragma=foreign_keys(1)",
		"_pragma=busy_timeout": "_pragma=busy_timeout(5000)",
	} {
		if strings.Contains(dsn, param) {
			continue
		}
		if strings.Contains(dsn, "?") {
			dsn += "&" + value
		} else {
			dsn += "?" + value
		}
	}
	return dsn
}

// IsNotFound reports whether a query found nothing.
func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }

// IsDuplicate reports whether a write hit a unique constraint.
//
// GORM's TranslateError covers the common paths; the string fallbacks catch
// the cases where a driver reports the violation without a code we recognise.
func IsDuplicate(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key value") ||
		strings.Contains(msg, "unique_violation")
}

// IsForeignKeyViolation reports whether a write referenced a missing row.
func IsForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrForeignKeyViolated) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "foreign key constraint") ||
		strings.Contains(msg, "violates foreign key")
}

// newLogger bridges GORM's logging onto slog at a sensible volume.
func newLogger(opts Options) logger.Interface {
	level := logger.Warn
	if opts.Debug {
		level = logger.Info
	}
	threshold := opts.SlowQueryThreshold
	if threshold == 0 {
		threshold = 500 * time.Millisecond
	}
	return logger.New(slogWriter{log: opts.Log}, logger.Config{
		SlowThreshold:             threshold,
		LogLevel:                  level,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      !opts.Debug,
	})
}

type slogWriter struct{ log *slog.Logger }

func (w slogWriter) Printf(format string, args ...any) {
	if w.log == nil {
		return
	}
	w.log.Warn(strings.TrimSpace(fmt.Sprintf(format, args...)), "source", "gorm")
}
