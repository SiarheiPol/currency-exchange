// Package apilayer implements ratesprovider.RatesProvider against the
// apilayer-family currency-live endpoint.
package apilayer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/ratesprovider"
)

// Compile-time assertion that Provider satisfies RatesProvider.
var _ ratesprovider.RatesProvider = (*Provider)(nil)

// Provider fetches exchange rates from the apilayer /live endpoint.
// Callers initialise fields directly; nil HTTPClient falls back to
// http.DefaultClient. Clock is reserved for future use and is unused today.
type Provider struct {
	APIKey     string
	BaseURL    string // e.g. "https://api.currencylayer.com" — no trailing slash
	HTTPClient *http.Client
	Clock      clock.Clock // reserved; unused this iteration
}

type apiResponse struct {
	Success   bool                       `json:"success"`
	Timestamp int64                      `json:"timestamp"`
	Source    string                     `json:"source"`
	Quotes    map[string]decimal.Decimal `json:"quotes"`
	Error     *struct {
		Code int    `json:"code"`
		Type string `json:"type"`
		Info string `json:"info"`
	} `json:"error"`
}

// FetchPairs fetches exchange rates for the given pairs. It groups pairs by
// base currency, issues one GET /live request per unique base in lexical
// order, and merges the results. On any error it fails fast and returns a
// *ratesprovider.ProviderError with the appropriate Code.
func (p *Provider) FetchPairs(ctx context.Context, pairs []ratesprovider.Pair) (ratesprovider.FetchResult, error) {
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	// Group pairs by base → unique targets.
	byBase := make(map[string]map[string]struct{})
	for _, pair := range pairs {
		if _, ok := byBase[pair.Base]; !ok {
			byBase[pair.Base] = make(map[string]struct{})
		}
		byBase[pair.Base][pair.Quote] = struct{}{}
	}

	// Sort bases for deterministic call order.
	bases := make([]string, 0, len(byBase))
	for base := range byBase {
		bases = append(bases, base)
	}
	slices.Sort(bases)

	result := ratesprovider.FetchResult{
		Quotes: make(map[ratesprovider.Pair]ratesprovider.Quote),
	}

	for _, base := range bases {
		targets := byBase[base]
		targetList := make([]string, 0, len(targets))
		for t := range targets {
			targetList = append(targetList, t)
		}

		obs.LogUpstreamCallStarted(ctx, "apilayer", targetList)
		start := time.Now()

		// Build request URL.
		params := url.Values{}
		params.Set("access_key", p.APIKey)
		params.Set("source", base)
		params.Set("currencies", strings.Join(targetList, ","))
		rawURL := p.BaseURL + "/live?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			duration := time.Since(start)
			provErr := &ratesprovider.ProviderError{
				Code:    "transient",
				Cause:   err,
				Message: "http request failed",
			}
			obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", "transient").Inc()
			obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
			obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, provErr)
			return ratesprovider.FetchResult{}, provErr
		}

		resp, err := client.Do(req)
		if err != nil {
			duration := time.Since(start)
			provErr := &ratesprovider.ProviderError{
				Code:    "transient",
				Cause:   err,
				Message: "http request failed",
			}
			obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", "transient").Inc()
			obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
			obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, provErr)
			return ratesprovider.FetchResult{}, provErr
		}

		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			duration := time.Since(start)
			provErr := &ratesprovider.ProviderError{
				Code:     "transient",
				HTTPCode: resp.StatusCode,
				Message:  "http error response",
			}
			obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", "transient").Inc()
			obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
			obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, provErr)
			return ratesprovider.FetchResult{}, provErr
		}

		var apiResp apiResponse
		if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
			_ = resp.Body.Close()
			duration := time.Since(start)
			provErr := &ratesprovider.ProviderError{
				Code:    "transient",
				Cause:   err,
				Message: "malformed response body",
			}
			obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", "transient").Inc()
			obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
			obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, provErr)
			return ratesprovider.FetchResult{}, provErr
		}
		_ = resp.Body.Close()

		if !apiResp.Success {
			apiCode := 0
			var message string
			if apiResp.Error != nil {
				apiCode = apiResp.Error.Code
				message = apiResp.Error.Info
			}
			var outcome string
			switch apiCode {
			case 104:
				outcome = "quota_exceeded"
			default:
				outcome = "permanent"
			}
			duration := time.Since(start)
			provErr := &ratesprovider.ProviderError{
				Code:    outcome,
				APICode: apiCode,
				Message: message,
			}
			obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", outcome).Inc()
			obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
			obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, provErr)
			return ratesprovider.FetchResult{}, provErr
		}

		// Parse quotes: each key is <base><target> (6 chars).
		fetchedAt := time.Unix(apiResp.Timestamp, 0)
		for key, price := range apiResp.Quotes {
			if len(key) != 6 {
				obs.RatesProviderResponseAnomaliesTotal.WithLabelValues("apilayer", "malformed_quote_key").Inc()
				obs.LogProviderResponseAnomaly(ctx, "apilayer", "malformed_quote_key", key)
				continue
			}
			keyBase := key[:3]
			keyTarget := key[3:]
			if keyBase != base {
				continue
			}
			pair := ratesprovider.Pair{Base: keyBase, Quote: keyTarget}
			result.Quotes[pair] = ratesprovider.Quote{
				Pair:      pair,
				Price:     price,
				FetchedAt: fetchedAt,
			}
		}

		duration := time.Since(start)
		obs.RatesProviderRequestsTotal.WithLabelValues("apilayer", "ok").Inc()
		obs.RatesProviderRequestDurationSeconds.WithLabelValues("apilayer").Observe(duration.Seconds())
		obs.LogUpstreamCallFinished(ctx, "apilayer", targetList, duration, nil)
	}

	// Compute Missing: deduplicated set of input pairs absent from Quotes.
	seen := make(map[ratesprovider.Pair]struct{})
	for _, pair := range pairs {
		if _, found := result.Quotes[pair]; !found {
			if _, already := seen[pair]; !already {
				seen[pair] = struct{}{}
				result.Missing = append(result.Missing, pair)
			}
		}
	}

	// Nil map → nil (keep zero value for Quotes when empty).
	if len(result.Quotes) == 0 {
		result.Quotes = nil
	}

	// Missing nil when empty per convention.
	if len(result.Missing) == 0 {
		result.Missing = nil
	}

	return result, nil
}
