// A load probe that emits one compact JSON line of results — the measurement
// primitive the chaos experiments are built on. Unlike autocannon/graphql.js
// (which prints a human summary), this is meant to be captured and parsed while
// something is being killed, scaled, or restarted underneath it.
//
//   TARGET=http://localhost:8088/graphql CONNECTIONS=40 DURATION=30 \
//     node load/chaos/probe.mjs
//
// Output (stdout, last line): {"rps":..,"p50":..,"p99":..,"max":..,
//   "served2xx":..,"limited4xx":..,"broke5xx":..,"errors":..,"timeouts":..}
//
// "broke" (5xx + transport errors + timeouts) is the number that matters during
// chaos: it is the count of requests the platform actually failed to serve, as
// opposed to 429s (the limiter working) which are not failures.

import autocannon from "autocannon";

const URL = process.env.TARGET || "http://localhost:8088/graphql";
const DURATION = Number(process.env.DURATION || 20);
const CONNECTIONS = Number(process.env.CONNECTIONS || 40);

// A read-only mix: leaderboard and the opening explorer are Postgres-backed,
// health is a pure liveness check. No writes, so the probe is safe to run at
// high concurrency and repeatedly.
const queries = [
  { query: "{ leaderboard(limit: 20) { rank username elo gamesPlayed } }" },
  { query: "{ openingExplorer(limit: 8) { totalGames moves { san total } } }" },
  { query: "{ health }" },
];

autocannon(
  {
    url: URL,
    connections: CONNECTIONS,
    duration: DURATION,
    requests: queries.map((q) => ({
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(q),
    })),
  },
  (err, r) => {
    if (err) {
      console.error(JSON.stringify({ fatal: String(err) }));
      process.exit(1);
    }
    const out = {
      rps: Math.round(r.requests.average),
      p50: r.latency.p50,
      p99: r.latency.p99,
      max: r.latency.max,
      served2xx: r["2xx"] || 0,
      limited4xx: r["4xx"] || 0,
      broke5xx: r["5xx"] || 0,
      errors: r.errors || 0,
      timeouts: r.timeouts || 0,
    };
    out.broke = out.broke5xx + out.errors + out.timeouts;
    console.log(JSON.stringify(out));
  },
);
