// Profile 2 — Read storm.
//
// Sends GET /quotes/latest at high RPS (default 500 req/s) to verify that
// the caching layer (Cache-Control, ETag) holds up and that read scalability
// is maintained under sustained pressure.
import http from "k6/http";
import { check } from "k6";
import { BASE_URL, PAIRS, pickRandom } from "./common.js";

// See profile1.js for the VU-sizing rationale. Default 100/200 fits ~500 RPS
// with sub-50ms p95; bump LOADTEST_VUS above 500 RPS.
const PREALLOCATED_VUS = parseInt(__ENV.LOADTEST_VUS) || 100;
const MAX_VUS = Math.max(PREALLOCATED_VUS * 2, 200);

export const options = {
  scenarios: {
    read_storm: {
      executor: "constant-arrival-rate",
      rate: __ENV.LOADTEST_RATE || 500,
      timeUnit: "1s",
      duration: __ENV.LOADTEST_DURATION || "30s",
      preAllocatedVUs: PREALLOCATED_VUS,
      maxVUs: MAX_VUS,
    },
  },
  thresholds: {
    "http_req_failed":                             ["rate<0.01"],
    "http_req_duration{name:GET /quotes/latest}":  ["p(99)<200"],
    "checks":                                       ["rate>0.99"],
  },
};

export default function () {
  const pair = pickRandom(PAIRS);
  const res = http.get(
    `${BASE_URL}/quotes/latest?base=${pair.base}&quote=${pair.quote}`,
    { tags: { name: "GET /quotes/latest" } }
  );
  check(res, {
    "status is 200": (r) => r.status === 200,
    'Cache-Control starts with "public"': (r) =>
      r.headers["Cache-Control"] &&
      r.headers["Cache-Control"].startsWith("public"),
    "ETag header is non-empty": (r) =>
      r.headers["Etag"] && r.headers["Etag"].length > 0,
  });
}
