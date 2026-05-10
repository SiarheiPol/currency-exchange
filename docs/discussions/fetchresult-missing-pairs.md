# FetchResult.Missing — replace Errors map with Missing slice

Plan-change for `internal/ratesprovider`: replace `FetchResult.Errors map[Pair]*ProviderError` with `FetchResult.Missing []Pair`. Code refactor follows in a separate TDD-cycle commit; this document and the accompanying discussion-doc edits land first.

## Why

`FetchResult.Errors` was originally designed to carry per-pair failures from the upstream provider. Empirical investigation of the apilayer-family API (currencylayer / fixer / exchangeratesapi.io — all share the same wire format) confirms there are **no per-pair errors at the protocol level**:

- `success: false` is a batch-level failure, mapped to a returned Go `error` (typed `*ProviderError`).
- On `success: true`, invalid or unsupported currencies are **silently dropped** from the `quotes` map. No per-pair error code is returned by the API.

Verified live against currencylayer on 2026-05-10:

```
GET /live?currencies=ZZZ,EUR  →  {success:true, quotes:{USDEUR:0.84804}}
```

`ZZZ` is absent from the response with no diagnostic. The only way our client learns "the upstream did not return this pair" is by diffing the requested pair list against `quotes`-map keys.

Every entry that would have been written to `FetchResult.Errors` is therefore **synthesised by our client** with a constant shape: `&ProviderError{Code: "permanent", Message: "missing in upstream response"}`. The `*ProviderError` type carries no information beyond "this pair was requested and is absent from the response."

`Missing []Pair` is the simplest type that conveys exactly that.

## Before / after

**Before** (`internal/ratesprovider/provider.go`):

```go
type FetchResult struct {
    Quotes map[Pair]Quote
    Errors map[Pair]*ProviderError
}
```

**After:**

```go
type FetchResult struct {
    // Quotes holds successfully fetched exchange rates, keyed by Pair.
    // Nil or empty when no pair succeeded.
    Quotes map[Pair]Quote
    // Missing lists every pair from the caller's input slice that is absent
    // from Quotes. The apilayer-family silently drops unsupported or unknown
    // currency codes rather than returning a per-pair error; Missing is how
    // the caller detects these silent drops.
    //
    // Entries are deduplicated — at most one entry per unique missing pair,
    // regardless of duplicates in the input slice. Order is unspecified;
    // callers that need deterministic order must sort.
    Missing []Pair
}
```

The `RatesProvider.FetchPairs` interface comment "Per-pair failures are communicated via `FetchResult.Errors`" becomes "Pairs that the upstream did not return are listed in `FetchResult.Missing`."

## Semantic contract for `Missing`

- `Missing` contains every `Pair` from the `[]Pair` argument to `FetchPairs` that is not a key in the same `FetchResult.Quotes`.
- Entries are deduplicated. If the caller supplies the same pair twice in the input slice, it appears in `Missing` at most once.
- Order is unspecified. Tests that assert on `Missing` must sort or use `require.ElementsMatch`.
- `Missing` is `nil` (not an empty slice) when every requested pair appears in `Quotes`. Mirrors the existing convention for `Quotes` ("nil or empty when no pair succeeded").
- A `Missing` entry carries no classification. The worker treats every missing pair as a permanent failure for the corresponding job, per `resilience.md` row "`success: true`, requested pair absent from response."

## Why now

`fakeRatesProvider` is the next Stage 3 iteration. It must be written against the final `FetchResult` shape to avoid a follow-up refactor of the fake itself. Doing this rename first means the fake, the worker, and the real `apilayerProvider` all use one consistent type.

## Out of scope

- Changes to `ProviderError` itself — it remains the type returned as the batch-level Go `error`, with the same `Code` / `HTTPCode` / `APICode` / `Message` / `Cause` fields.
- `fakeRatesProvider`, `apilayerProvider`, worker wiring — all subsequent Stage 3 items.
- `QuoteRepo` interface — unrelated.

## Future provider with real per-pair errors

If a future provider with a per-pair error protocol ever appears, that is a separate plan-change. At that point we will reintroduce a per-pair classification field (likely `Errors map[Pair]*ProviderError` again, or a richer alternative) — but only when there is a concrete provider that produces such errors. Until then, `Missing []Pair` is the honest representation of what apilayer-family providers tell us.
