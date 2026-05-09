package ratesprovider_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/ratesprovider"
)

// TestProviderError_IsTransient verifies the two-code allowlist: only
// "transient" and "quota_exceeded" return true; all other values (including
// the empty string and unknown strings) return false.
func TestProviderError_IsTransient(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		code string
		want bool
	}{
		{name: "transient", code: "transient", want: true},
		{name: "quota_exceeded", code: "quota_exceeded", want: true},
		{name: "permanent", code: "permanent", want: false},
		{name: "zero_value", code: "", want: false},
		{name: "unknown", code: "rate_limited", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pe := &ratesprovider.ProviderError{Code: tc.code}
			require.Equal(t, tc.want, pe.IsTransient(),
				"ProviderError{Code: %q}.IsTransient() = %v, want %v",
				tc.code, pe.IsTransient(), tc.want)
		})
	}
}
