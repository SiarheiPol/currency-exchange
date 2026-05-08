package obs_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/obs"
)

// Compilation guard — each Ev* constant must exist, be exported, and be a string.
// A compile error here means the constants are missing from package obs.
var (
	_ string = obs.EvHTTPRequestReceived
	_ string = obs.EvHTTPRequestCompleted
	_ string = obs.EvJobReserved
	_ string = obs.EvJobCompleted
	_ string = obs.EvJobRescheduled
	_ string = obs.EvJobFailed
	_ string = obs.EvSchedulerTick
	_ string = obs.EvUpstreamCallStarted
	_ string = obs.EvUpstreamCallFinished
	_ string = obs.EvCoalescingCollapsed
)

// TestEvConstants_Values asserts that every Ev* constant carries the exact
// canonical string value defined in the event catalog.
func TestEvConstants_Values(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"EvHTTPRequestReceived", obs.EvHTTPRequestReceived, "http request received"},
		{"EvHTTPRequestCompleted", obs.EvHTTPRequestCompleted, "http request completed"},
		{"EvJobReserved", obs.EvJobReserved, "job reserved"},
		{"EvJobCompleted", obs.EvJobCompleted, "job completed"},
		{"EvJobRescheduled", obs.EvJobRescheduled, "job rescheduled"},
		{"EvJobFailed", obs.EvJobFailed, "job failed"},
		{"EvSchedulerTick", obs.EvSchedulerTick, "scheduler tick"},
		{"EvUpstreamCallStarted", obs.EvUpstreamCallStarted, "upstream call started"},
		{"EvUpstreamCallFinished", obs.EvUpstreamCallFinished, "upstream call finished"},
		{"EvCoalescingCollapsed", obs.EvCoalescingCollapsed, "coalescing collapsed"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// TestEvConstants_NoDuplicates asserts that all 10 event constant values are
// distinct — no two constants share the same string.
func TestEvConstants_NoDuplicates(t *testing.T) {
	t.Parallel()

	all := []string{
		obs.EvHTTPRequestReceived,
		obs.EvHTTPRequestCompleted,
		obs.EvJobReserved,
		obs.EvJobCompleted,
		obs.EvJobRescheduled,
		obs.EvJobFailed,
		obs.EvSchedulerTick,
		obs.EvUpstreamCallStarted,
		obs.EvUpstreamCallFinished,
		obs.EvCoalescingCollapsed,
	}

	seen := make(map[string]struct{}, len(all))
	for _, v := range all {
		seen[v] = struct{}{}
	}

	assert.Len(t, seen, len(all), "event constants must all have distinct values")
}

// TestEvConstants_LowerCaseNonEmpty asserts that every event constant value is
// non-empty and contains only lower-case characters (i.e. value == strings.ToLower(value)).
func TestEvConstants_LowerCaseNonEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
	}{
		{"EvHTTPRequestReceived", obs.EvHTTPRequestReceived},
		{"EvHTTPRequestCompleted", obs.EvHTTPRequestCompleted},
		{"EvJobReserved", obs.EvJobReserved},
		{"EvJobCompleted", obs.EvJobCompleted},
		{"EvJobRescheduled", obs.EvJobRescheduled},
		{"EvJobFailed", obs.EvJobFailed},
		{"EvSchedulerTick", obs.EvSchedulerTick},
		{"EvUpstreamCallStarted", obs.EvUpstreamCallStarted},
		{"EvUpstreamCallFinished", obs.EvUpstreamCallFinished},
		{"EvCoalescingCollapsed", obs.EvCoalescingCollapsed},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, tc.value, "event constant must not be empty")
			assert.Equal(t, strings.ToLower(tc.value), tc.value, "event constant must be lower-case")
		})
	}
}
