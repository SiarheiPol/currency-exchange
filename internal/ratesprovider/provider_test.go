package ratesprovider_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/ratesprovider"
)

// TestProviderError_Error_APICode verifies that Error() includes the
// "api_code=<n>" token only when APICode is non-zero.
func TestProviderError_Error_APICode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		pe             *ratesprovider.ProviderError
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:           "zero_api_code_omitted",
			pe:             &ratesprovider.ProviderError{Code: "transient", APICode: 0, Message: "timeout"},
			mustContain:    []string{"transient", "timeout"},
			mustNotContain: []string{"api_code="},
		},
		{
			name:           "nonzero_api_code_included",
			pe:             &ratesprovider.ProviderError{Code: "transient", HTTPCode: 200, APICode: 104, Message: "quota exceeded"},
			mustContain:    []string{"api_code=104"},
			mustNotContain: nil,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := tc.pe.Error()
			for _, sub := range tc.mustContain {
				require.True(t, strings.Contains(got, sub),
					"Error() = %q, want it to contain %q", got, sub)
			}
			for _, sub := range tc.mustNotContain {
				require.False(t, strings.Contains(got, sub),
					"Error() = %q, want it NOT to contain %q", got, sub)
			}
		})
	}
}

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
