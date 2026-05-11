// Base URL the scripts use. Inside the compose network, the service is
// reachable as "server"; the host port mapping (localhost:8080) is not
// relevant from inside the k6 container.
export const BASE_URL = "http://server:8080";

// All directional currency pairs in the whitelist (USD, EUR, MXN).
export const PAIRS = [
  { base: "USD", quote: "EUR" },
  { base: "USD", quote: "MXN" },
  { base: "EUR", quote: "USD" },
  { base: "EUR", quote: "MXN" },
  { base: "MXN", quote: "USD" },
  { base: "MXN", quote: "EUR" },
];

export const JSON_HEADERS = { "Content-Type": "application/json" };

// Pick one element at random.
export function pickRandom(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}
