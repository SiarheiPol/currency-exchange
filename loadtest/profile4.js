// Profile 4 — Coalescing stress.
//
// Bursts 100 concurrent POST /quotes/refresh on a single fixed pair (USD/EUR)
// and asserts that the upstream provider counter incremented by at most 2 — the
// architectural invariant that requests within one coalescing window collapse
// into a single upstream call (one extra allowed for a bucket-boundary cross).
//
// IMPORTANT — implicit coupling: this test assumes COALESCING_WINDOW_SECONDS
// >= 10 in the running server. The burst takes ~5 s; with smaller windows the
// requests would span multiple buckets and produce more upstream calls than
// the `delta <= 2` threshold accepts. docker-compose.yml hardcodes the server
// env to `COALESCING_WINDOW_SECONDS=30`, so the assumption holds out of the
// box. If you later override it via shell env or .env file, keep this floor
// in mind.
import http from "k6/http";
import { check } from "k6";
import { BASE_URL, JSON_HEADERS } from "./common.js";

export const options = {
  scenarios: {
    coalescing_burst: {
      executor: "per-vu-iterations",
      vus: 100,
      iterations: 1,
      maxDuration: "10s",
    },
  },
  thresholds: {
    checks: ["rate>0.99"],
  },
};

// Parse the sum of rates_provider_requests_total{...outcome="ok"...} from
// Prometheus text exposition format.
function parseProviderOkCount(metricsText) {
  let sum = 0;
  const lines = metricsText.split("\n");
  for (const line of lines) {
    if (
      line.startsWith("rates_provider_requests_total{") &&
      line.includes('outcome="ok"')
    ) {
      const parts = line.split(" ");
      const value = parseFloat(parts[parts.length - 1]);
      if (!isNaN(value)) {
        sum += value;
      }
    }
  }
  return sum;
}

export function setup() {
  const res = http.get(`${BASE_URL}/metrics`);
  const baselineOk = parseProviderOkCount(res.body);
  console.log(`setup: baseline rates_provider_requests_total{outcome="ok"} = ${baselineOk}`);
  return { baselineOk };
}

export default function (data) {
  const res = http.post(
    `${BASE_URL}/quotes/refresh`,
    JSON.stringify({ base: "USD", quote: "EUR" }),
    { headers: JSON_HEADERS }
  );
  check(res, {
    "POST /quotes/refresh status 202": (r) => r.status === 202,
  });
}

export function teardown(data) {
  const res = http.get(`${BASE_URL}/metrics`);
  const currentOk = parseProviderOkCount(res.body);
  const delta = currentOk - data.baselineOk;
  console.log(
    `teardown: current ok=${currentOk}, baseline ok=${data.baselineOk}, delta=${delta}`
  );
  check(null, {
    "provider delta <= 2": () => delta <= 2,
  });
}
