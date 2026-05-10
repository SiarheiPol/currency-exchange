# apilayer-family RatesProvider — reference

Authoritative reference for implementing `apilayerProvider` against currencylayer / exchangeratesapi.io / fixer (shared API shape). Source: currencylayer.com/documentation + verified live with a Free-plan key on 2026-05-10.

When this doc and `currencylayer.com/documentation` disagree, this doc wins — the published docs misrepresent at least two important behaviours (see §"Empirical findings").

## Endpoint

```
GET https://api.currencylayer.com/live
  ?access_key=<KEY>           required, query param
  &source=<ISO_4217>           optional, default USD
  &currencies=<CSV_ISO_4217>   optional, default = all supported
```

Auth: query param only on currencylayer (no header auth). Fixer / exchangeratesapi.io siblings use an `apikey` header — for MVP we target currencylayer only and the implementation may hard-code the query-param style. Always HTTP 200 except for 5xx / network failures.

## Success response

```json
{
  "success": true,
  "timestamp": 1778354527,
  "source": "USD",
  "quotes": {"USDEUR": 0.84804, "USDMXN": 17.177604}
}
```

- `quotes` keys are `<SOURCE><TARGET>` concatenated (e.g. `USDEUR`). The first three letters are the source, the next three are the target. Pair parsing is required.
- `timestamp` is the upstream observation time as Unix seconds (integer). Use as `Quote.FetchedAt` for every quote in the response.
- Source currency itself is **not echoed in `quotes`** — no `USDUSD` appears even if the source is included in `currencies`.

## Error response

Always HTTP 200, `success: false`. **No per-currency errors** are ever returned at the protocol level. Verified live with an invalid `access_key` on 2026-05-10:

```json
{
  "success": false,
  "error": {
    "code": 101,
    "type": "invalid_access_key",
    "info": "You have not supplied a valid API Access Key. [Technical Support: support@apilayer.com]"
  }
}
```

The `error` object always has `code` (integer) and `info` (string). The `type` field (string snake-case identifier, e.g. `invalid_access_key`) is also present empirically; the public docs do not document it. Parser should accept its presence but is not required to use its value — `code` is the canonical classifier.

| API code | Meaning | `ProviderError.Code` |
|---|---|---|
| 101 | Missing/invalid `access_key` | `permanent` |
| 102 | Account inactive | `permanent` |
| 103 | Non-existent function | `permanent` |
| **104** | **Monthly quota reached** | **`quota_exceeded`** (transient) |
| 105 | Plan does not support function | `permanent` |
| 106 | No results | `permanent` |
| 201 | Invalid source currency | `permanent` |
| 202 | One or more invalid currency codes | `permanent` (rare — see empirics #2) |
| 404 | Resource not found | `permanent` |
| HTTP 5xx | Server error | `transient` |
| network/timeout | Connection failure | `transient` |
| malformed JSON | Provider bug | `transient` |

`*ProviderError` carries `APICode = error.code` for diagnosis; `HTTPCode` is whatever the response carried (almost always 200 on application-level errors). `IsTransient()` returns true for `transient` and `quota_exceeded`.

## Empirical findings (contradict published docs)

Verified live against `/live` with a Free-plan key on 2026-05-10:

**1. Any `source` works on Free.** Docs claim "USD-source only on Free → code 105". Reality: `source=EUR` and `source=MXN` returned `success:true` with `EURUSD/EURMXN` and `MXNUSD/MXNEUR` respectively. *Strategy: use any source freely; ignore the documented restriction.*

**2. Invalid currencies silently dropped, no code 202.** Docs claim "one or more invalid → reject whole batch with code 202". Reality: `currencies=ZZZ,EUR` returned `{success:true, quotes:{USDEUR:0.84804}}` — `ZZZ` just absent. *Strategy: the client computes `FetchResult.Missing` as the diff between requested pairs and returned `quotes` keys. This is the dominant failure mode, not an edge case.*

**3. Reciprocal ≠ direct.** `1 / USDEUR (0.84804) = 1.179190…` vs direct `EURUSD = 1.179189`. Diverges at the 6th decimal (~10⁻⁶). The provider rounds each side independently — this is real data divergence, not floating-point noise. *Strategy: never derive `EURUSD` from `1/USDEUR`.*

**4. Cross-rate ≠ direct.** `USDMXN/USDEUR = 17.177604/0.84804 = 20.255653…` vs direct `EURMXN = 20.255648`. Same ~5×10⁻⁶ divergence. *Strategy: never derive `EURMXN` from `USDMXN/USDEUR`.*

**5. Invalid `access_key` returns code 101 with HTTP 200.** `?access_key=INVALID_KEY_TEST` returned the JSON shown in §"Error response" above. Confirms the `success:false` envelope is the carrier even for total-auth-failure cases — the client must always parse the body, never trust HTTP status alone.

## Strategy A — locked

Always direct calls, never derive.

For a `FetchPairs(pairs)` batch:

1. **Group `pairs` by `Base`.** For whitelist `[USD, EUR, MXN]`: 3×3 − 3 = **6 directional pairs** total, partitioned into **3 unique bases**, each with 2 targets. Issues **3 HTTP calls per batch**, returning **2 pairs per call** (the source itself is not echoed). Total: 6 pairs in 3 calls.
2. **Per unique base, one HTTP call:** `GET /live?access_key=<KEY>&source=<base>&currencies=<comma-joined targets>`.
3. **Parse each `quotes` key.** First 3 chars → base, next 3 → target → `Pair{Base, Quote}`. Build `result.Quotes[pair] = Quote{Pair: pair, Price: <decimal>, FetchedAt: time.Unix(timestamp, 0)}`.
4. **Diff request vs response.** Any pair from the input slice absent from `result.Quotes` is appended (deduplicated) to `result.Missing`.
5. **On `success: false`:** parse `error.code`, map per the table above to `ProviderError.Code`, return as the batch-level Go `error` — i.e. `(FetchResult{}, *ProviderError{...})`. Per-call: a single failed call may either fail the whole batch or be partial-batch handled — implementation choice for the iteration that introduces the provider.

## Quota math

`len(unique_bases) × ticks/month`. For whitelist `[USD, EUR, MXN]` and T=24h: `3 × 30 = 90 calls/month`. Free plan = 100 calls/month. Tight but workable for MVP. Reviewers supply their own key; tests use `fakeRatesProvider`; no production deployment.

## Out of scope for this doc

- `apikey` header auth used by fixer / exchangeratesapi.io siblings. We target currencylayer only.
- Historical (`/historical`), conversion (`/convert`), time-frame (`/timeframe`), or list (`/list`) endpoints. We use only `/live`.
- Provider rotation, fallback, multiple providers in parallel. One provider, one MVP.
- Circuit breakers, rate-limiting on our side. Stage 6 territory.

## See also

- `background-mechanism.md` — high-level worker / provider flow.
- `resilience.md` — full error classification table and worker dispatch.
- `fetchresult-missing-pairs.md` — `FetchResult.Missing` semantics.
- `implementation-roadmap.md` Stage 3 — checkbox order.
