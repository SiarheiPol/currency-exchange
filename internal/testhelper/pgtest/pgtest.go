//go:build integration

// Package pgtest provides a test helper that starts a Postgres container,
// applies all migrations, and returns an isolated *pgxpool.Pool for use in
// integration tests.
package pgtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// dbCounter ensures each NewDB call gets a unique schema name even when called
// multiple times within the same test.
var dbCounter atomic.Uint64

var nonAlphanumRE = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// sanitize converts a test name into a valid Postgres identifier fragment.
// It lowercases, replaces non-alphanumeric runs with underscores, and
// truncates to 63 characters.
func sanitize(name string) string {
	s := nonAlphanumRE.ReplaceAllString(name, "_")
	// lower-case
	lower := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower = append(lower, c)
	}
	result := string(lower)
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}

// findMigrationsDir walks up from the current working directory until it finds
// a go.mod file, then returns the migrations/ subdirectory.
func findMigrationsDir() string {
	dir, err := os.Getwd()
	if err != nil {
		panic(fmt.Sprintf("pgtest: os.Getwd: %v", err))
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return filepath.Join(dir, "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("pgtest: go.mod not found while searching for migrations dir")
		}
		dir = parent
	}
}

// NewDB starts a Postgres 16 testcontainer, applies all migrations into a
// fresh, test-specific schema, and returns a *pgxpool.Pool connected to that
// schema. Cleanup (drop schema, close pool, stop container) is registered via
// t.Cleanup.
func NewDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx := context.Background()

	pgContainer, err := tcpostgres.Run(ctx,
		"postgres:16",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("pgtest: start container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("pgtest: connection string: %v", err)
	}

	n := dbCounter.Add(1)
	base := sanitize(t.Name())
	suffix := fmt.Sprintf("_%d", n)
	// Ensure total length (including "test_" prefix and suffix) stays <= 63.
	maxBase := 63 - len("test_") - len(suffix)
	if len(base) > maxBase {
		base = base[:maxBase]
	}
	schemaName := fmt.Sprintf("test_%s%s", base, suffix)

	// Create the test schema.
	basePool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgtest: create base pool: %v", err)
	}
	_, err = basePool.Exec(ctx, fmt.Sprintf("CREATE SCHEMA %s", schemaName))
	basePool.Close()
	if err != nil {
		t.Fatalf("pgtest: create schema %s: %v", schemaName, err)
	}

	// Build a connection string with search_path set to the test schema.
	// connStr is like "postgres://user:pass@host:port/db?sslmode=disable"
	schemaConnStr := connStr + "&search_path=" + schemaName

	// golang-migrate's pgx/v5 driver expects the scheme "pgx5://" instead of
	// "postgres://".
	migrateConnStr := strings.Replace(schemaConnStr, "postgres://", "pgx5://", 1)

	// Apply migrations into the test schema.
	migrationsDir := findMigrationsDir()
	m, err := migrate.New("file://"+migrationsDir, migrateConnStr)
	if err != nil {
		t.Fatalf("pgtest: migrate.New: %v", err)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("pgtest: migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		t.Fatalf("pgtest: migrate close src: %v", srcErr)
	}
	if dbErr != nil {
		t.Fatalf("pgtest: migrate close db: %v", dbErr)
	}

	// Create the pool that tests will use.
	// Configure the TimestamptzCodec to scan timestamps as UTC so that
	// time.Time values returned from TIMESTAMPTZ columns are always UTC,
	// regardless of the server or host locale.
	poolCfg, err := pgxpool.ParseConfig(schemaConnStr)
	if err != nil {
		t.Fatalf("pgtest: parse pool config: %v", err)
	}
	poolCfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name:  "timestamptz",
			OID:   pgtype.TimestamptzOID,
			Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("pgtest: create test pool: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()

		cleanCtx := context.Background()
		dropPool, dropErr := pgxpool.New(cleanCtx, connStr)
		if dropErr == nil {
			_, _ = dropPool.Exec(cleanCtx,
				fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schemaName))
			dropPool.Close()
		}
		if termErr := pgContainer.Terminate(cleanCtx); termErr != nil {
			t.Logf("pgtest: terminate container: %v", termErr)
		}
	})

	return pool
}
