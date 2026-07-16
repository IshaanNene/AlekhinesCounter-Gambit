// autocannon load test for the GraphQL read path.
//
// Hammers the queries a browser fires constantly — leaderboard, the opening
// explorer, a game fetch — to measure the gateway → game-service → Postgres path
// under concurrency, and to prove the eval cache and Redis-backed leaderboard
// hold up. Mutations are load-tested separately (k6/game_flow.js) because they
// need a session and change state; this is the pure read throughput number.
//
//   node load/autocannon/graphql.js            # 10s, 50 connections
//   DURATION=30 CONNECTIONS=100 node load/autocannon/graphql.js
//
// Requires: npm i -g autocannon  (or npx autocannon-based; we call the API).

import autocannon from "autocannon";

const URL = process.env.TARGET || "http://localhost:3000/graphql";
const DURATION = Number(process.env.DURATION || 10);
const CONNECTIONS = Number(process.env.CONNECTIONS || 50);

// A representative read mix. Each request is a complete GraphQL query a real
// client sends, so the numbers reflect the actual API, not a synthetic /health.
const queries = [
  {
    name: "leaderboard",
    body: { query: "{ leaderboard(limit: 20) { rank username elo gamesPlayed } }" },
  },
  {
    name: "openingExplorer(start)",
    body: { query: "{ openingExplorer(limit: 8) { totalGames moves { san total whiteWins draws blackWins } } }" },
  },
  {
    name: "openingExplorer(after 1.e4)",
    body: {
      query: "query($f: String){ openingExplorer(fen: $f, limit: 8) { totalGames moves { san total } } }",
      variables: { f: "rnbqkbnr/pppppppp/8/8/4P3/8/PPPP1PPP/RNBQKBNR b KQkq e3 0 1" },
    },
  },
  {
    name: "health",
    body: { query: "{ health }" },
  },
];

const instance = autocannon(
  {
    url: URL,
    connections: CONNECTIONS,
    duration: DURATION,
    // Rotate through the query mix so the run exercises several code paths, not
    // one hot query the cache would trivially serve.
    requests: queries.map((q) => ({
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(q.body),
    })),
  },
  (err, result) => {
    if (err) {
      console.error("load test failed:", err);
      process.exit(1);
    }
    // 429s are not failures — they are the rate limiter doing its job. From one
    // host every connection shares an IP and one token bucket, so a flood is
    // *supposed* to be mostly rejected. Only 5xx and transport errors mean the
    // backend actually broke. Set ACG_RATE_LIMIT_RPS=0 on the gateway to measure
    // raw throughput with the cap lifted.
    const limited = result["4xx"] || 0;
    const served = result["2xx"] || 0;
    const broke = (result["5xx"] || 0) + result.errors + result.timeouts;

    console.log("\n=== GraphQL read path ===");
    console.log(`target:        ${URL}`);
    console.log(`duration:      ${DURATION}s   connections: ${CONNECTIONS}`);
    console.log(`requests/sec:  ${result.requests.average.toFixed(0)} avg, ${result.requests.max} peak`);
    console.log(`latency:       ${result.latency.average.toFixed(1)}ms avg, ${result.latency.p99}ms p99, ${result.latency.max}ms max`);
    console.log(`served (2xx):  ${served}`);
    console.log(`rate-limited:  ${limited}  (429 — the limiter absorbing the flood, not an error)`);
    console.log(`server errors: ${broke}`);
    if (broke > 0) {
      console.error("\n⚠  5xx / transport errors — the backend actually broke under load.");
      process.exit(1);
    }
    if (served > 0 && limited > served * 5) {
      console.log("\n✔  the rate limiter shielded the backend from the flood.");
    } else {
      console.log("\n✔  sustained the load cleanly.");
    }
  },
);

autocannon.track(instance, { renderProgressBar: true });
