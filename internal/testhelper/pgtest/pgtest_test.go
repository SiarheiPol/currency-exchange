//go:build integration

package pgtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/testhelper/pgtest"
)

// TestNewDB_AppliesMigrations confirms that the pool returned by NewDB has
// all expected tables, indexes, and constraints created by migrations.
func TestNewDB_AppliesMigrations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	pool := pgtest.NewDB(t)
	require.NotNil(t, pool)

	// quote_jobs table must exist.
	var quoteJobsRegclass *string
	err := pool.QueryRow(ctx, "SELECT to_regclass('quote_jobs')::text").Scan(&quoteJobsRegclass)
	require.NoError(t, err)
	assert.NotNil(t, quoteJobsRegclass, "quote_jobs table should exist")

	// quotes table must exist.
	var quotesRegclass *string
	err = pool.QueryRow(ctx, "SELECT to_regclass('quotes')::text").Scan(&quotesRegclass)
	require.NoError(t, err)
	assert.NotNil(t, quotesRegclass, "quotes table should exist")

	// Both indexes on quote_jobs must exist.
	rows, err := pool.Query(ctx,
		"SELECT indexname FROM pg_indexes WHERE tablename = 'quote_jobs'")
	require.NoError(t, err)
	defer rows.Close()

	var indexNames []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		indexNames = append(indexNames, name)
	}
	require.NoError(t, rows.Err())

	assert.Contains(t, indexNames, "quote_jobs_pending_idx",
		"index quote_jobs_pending_idx must exist")
	assert.Contains(t, indexNames, "quote_jobs_dedup_key_uidx",
		"index quote_jobs_dedup_key_uidx must exist")

	// last_error CHECK constraint: value longer than 4096 bytes must be rejected.
	longError := strings.Repeat("x", 4097)
	_, insertErr := pool.Exec(ctx,
		`INSERT INTO quote_jobs
			(id, currency, status, last_error, next_run_at, created_at, updated_at)
		VALUES
			('00000000-0000-0000-0000-000000000099', 'USD', 'pending', $1, NOW(), NOW(), NOW())`,
		longError,
	)
	assert.Error(t, insertErr,
		"INSERT with last_error > 4096 bytes should violate the CHECK constraint")
}

// TestNewDB_IsolatesSchemas confirms that two calls to NewDB return pools
// pointing at different schemas, and that data written via one pool is not
// visible via the other.
func TestNewDB_IsolatesSchemas(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db1 := pgtest.NewDB(t)
	db2 := pgtest.NewDB(t)

	// Schema names must differ.
	var schema1, schema2 string
	require.NoError(t, db1.QueryRow(ctx, "SELECT current_schema()").Scan(&schema1))
	require.NoError(t, db2.QueryRow(ctx, "SELECT current_schema()").Scan(&schema2))
	assert.NotEqual(t, schema1, schema2,
		"each call to NewDB should create a distinct schema")

	// A row inserted via db1 must not appear in db2.
	_, err := db1.Exec(ctx,
		`INSERT INTO quote_jobs
			(id, currency, status, next_run_at, created_at, updated_at)
		VALUES
			('00000000-0000-0000-0000-000000000001', 'USD', 'pending', NOW(), NOW(), NOW())`,
	)
	require.NoError(t, err)

	var count int
	require.NoError(t, db2.QueryRow(ctx, "SELECT count(*) FROM quote_jobs").Scan(&count))
	assert.Equal(t, 0, count,
		"row inserted via db1 must not be visible in db2's isolated schema")
}
