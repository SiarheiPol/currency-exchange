import http from "k6/http";
import { check } from "k6";
import { BASE_URL, PAIRS, JSON_HEADERS, pickRandom } from "./common.js";

export const options = {
  scenarios: {
    mixed_load: {
      executor: "constant-arrival-rate",
      rate: 50,
      timeUnit: "1s",
      duration: __ENV.LOADTEST_DURATION || "30s",
      preAllocatedVUs: 50,
      maxVUs: 100,
    },
  },
  thresholds: {
    http_req_failed: ["rate<0.01"],
    "http_req_duration{name:GET /quotes/latest}": ["p(99)<200"],
    "http_req_duration{name:POST /quotes/refresh}": ["p(99)<300"],
    checks: ["rate>0.99"],
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
    const res = http.post(
      `${BASE_URL}/quotes/refresh`,
      JSON.stringify({ base: pair.base, quote: pair.quote }),
      { headers: JSON_HEADERS, tags: { name: "POST /quotes/refresh" } }
    );
    check(res, {
      "POST /quotes/refresh status 202": (r) => r.status === 202,
    });
  }
}
