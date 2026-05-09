package obs_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/obs"
)

func TestLogger_CarriesRequestID(t *testing.T) {
	t.Parallel()

	id := "test-id"
	ctx := obs.WithRequestID(context.Background(), id)

	var buf bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&buf, nil))

	// Bind the base logger
	ctx = obs.WithLogger(ctx, l)

	// Get logger from ctx - it should now have the request_id
	gotLogger := obs.Logger(ctx)

	// Trigger a log
	gotLogger.Info("test message")

	output := buf.String()
	assert.Contains(t, output, `"request_id":"test-id"`)
	assert.Contains(t, output, `"msg":"test message"`)
}
