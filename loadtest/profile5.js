// Profile 5 — Failure injection (latency only).
//
// Scope: latency injection only. The full chaos menu (5xx, 401 auth,
// 202 silent drop, partial response, connection refused, malformed JSON)
// requires fakeprovider features that do not exist yet.
//
// Operator precondition for activating latency injection:
//   FAKE_LATENCY_MIN_MS=500 FAKE_LATENCY_MAX_MS=2000 docker compose up -d
//
// When FAKE_LATENCY_MIN_MS / FAKE_LATENCY_MAX_MS are both 0 (the default in
// docker-compose.yml) this profile runs as a baseline smoke test and all
// thresholds should still pass. The load-bearing assertion is that the
// thresholds also pass WITH latency injection active — the service's internal
// caching means most GET /quotes/latest responses are served from the DB
// layer and are not blocked behind slow upstream calls.
import http from "k6/http";
import { check } from "k6";
import { BASE_URL, PAIRS, JSON_HEADERS, pickRandom } from "./common.js";

export const options = {
  scenarios: {
    failure_injection: {
      executor: "constant-arrival-rate",
      rate: __ENV.LOADTEST_RATE || 25,
      timeUnit: "1s",
      duration: __ENV.LOADTEST_DURATION || "30s",
      preAllocatedVUs: 50,
      maxVUs: 100,
    },
  },
  thresholds: {
    "http_req_failed":                               ["rate<0.01"],
    "http_req_duration{name:GET /quotes/latest}":    ["p(99)<200"],
    "http_req_duration{name:POST /quotes/refresh}":  ["p(99)<300"],
    "checks":                                         ["rate>0.99"],
  },
};

export default function () {
  const pair = pickRandom(PAIRS);
  const roll = Math.random();

  if (roll < 0.8) {
    const res = http.get(
      `${BASE_URL}/quotes/latest?base=${pair.base}&quote=${pair.quote}`,
      { tags: { name: "GET /quotes/latest" } }
    );
    check(res, {
      "GET /quotes/latest status 200": (r) => r.status === 200,
    });
  } else {
    const body = JSON.stringify({ base: pair.base, quote: pair.quote });
    const res = http.post(`${BASE_URL}/quotes/refresh`, body, {
      headers: JSON_HEADERS,
      tags: { name: "POST /quotes/refresh" },
    });
    check(res, {
      "POST /quotes/refresh status 202": (r) => r.status === 202,
    });
  }
}
