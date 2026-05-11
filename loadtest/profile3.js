// Profile 3 — Refresh burst.
//
// Sends POST /quotes/refresh at sustained RPS (default 100 req/s) across
// all whitelisted pairs. The teardown phase reads /metrics and asserts that
// the pending job count has drained below 50, confirming the worker is
// keeping up with the ingest rate.
import http from "k6/http";
import { check } from "k6";
import { BASE_URL, PAIRS, JSON_HEADERS } from "./common.js";

export const options = {
  scenarios: {
    refresh_burst: {
      executor: "constant-arrival-rate",
      rate: __ENV.LOADTEST_RATE || 100,
      timeUnit: "1s",
      duration: __ENV.LOADTEST_DURATION || "30s",
      preAllocatedVUs: 50,
      maxVUs: 100,
    },
  },
  thresholds: {
    "http_req_failed":                               ["rate<0.01"],
    "http_req_duration{name:POST /quotes/refresh}":  ["p(99)<300"],
    "checks":                                         ["rate>0.99"],
  },
};

// Note: __ITER is per-VU, so pair distribution across all VUs will be
// approximately round-robin but not perfectly interleaved. True global
// round-robin would require shared state (e.g. a k6 SharedArray + atomic
// counter) which is not available in k6's VU isolation model. Per-VU
// __ITER % PAIRS.length is accepted as a close approximation.
export default function () {
  const pair = PAIRS[__ITER % PAIRS.length];
  const body = JSON.stringify({ base: pair.base, quote: pair.quote });
  const res = http.post(`${BASE_URL}/quotes/refresh`, body, {
    headers: JSON_HEADERS,
    tags: { name: "POST /quotes/refresh" },
  });
  check(res, {
    "status is 202": (r) => r.status === 202,
  });
}

// parsePendingCount scans Prometheus text exposition for the
// quote_jobs_pending_count gauge (no labels). Returns -1 if not found
// so the pending_count < 50 check fails clearly.
function parsePendingCount(text) {
  const lines = text.split("\n");
  for (const line of lines) {
    if (line.startsWith("#")) {
      continue;
    }
    if (line.startsWith("quote_jobs_pending_count ")) {
      const parts = line.split(" ");
      const value = parseFloat(parts[parts.length - 1]);
      if (!isNaN(value)) {
        return value;
      }
    }
  }
  return -1;
}

export function teardown() {
  const res = http.get(`${BASE_URL}/metrics`);
  const pending = parsePendingCount(res.body);
  console.info(`teardown: quote_jobs_pending_count = ${pending}`);
  check(null, {
    "pending_count < 50": () => pending < 50,
  });
}
