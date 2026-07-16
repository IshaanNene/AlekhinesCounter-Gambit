// k6 load test: the full authenticated write path.
//
// Each virtual user signs in as a guest, plays a complete game against the
// engine, and reads the result. This exercises the whole stack under load —
// gateway → game-service → Stockfish → session-manager → Postgres/Redis/Kafka —
// which is the number that actually matters: how many concurrent games the
// platform sustains, not how fast a static query returns.
//
//   k6 run load/k6/game_flow.js
//   k6 run -e VUS=50 -e DURATION=1m load/k6/game_flow.js

import http from "k6/http";
import { check, sleep } from "k6";
import { Trend, Rate, Counter } from "k6/metrics";

const GQL = __ENV.TARGET || "http://localhost:3000/graphql";

// Custom metrics, because k6's defaults measure HTTP, and we care about
// chess-level events: a completed game, an engine reply's latency.
const gamesCompleted = new Counter("games_completed");
const moveLatency = new Trend("move_latency_ms", true);
const gameErrors = new Rate("game_errors");

export const options = {
  scenarios: {
    games: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "10s", target: Number(__ENV.VUS || 20) }, // ramp up
        { duration: __ENV.DURATION || "30s", target: Number(__ENV.VUS || 20) }, // hold
        { duration: "5s", target: 0 }, // ramp down
      ],
    },
  },
  thresholds: {
    // A move (which includes an engine reply) should stay responsive under load.
    move_latency_ms: ["p(95)<3000"],
    game_errors: ["rate<0.01"],
    http_req_failed: ["rate<0.01"],
  },
};

// The Ruy Lopez, a few moves deep, then resign — a realistic short game that
// makes the engine actually think without running for minutes per VU.
const OPENING = ["e2e4", "g1f3", "f1b5", "e1g1", "d2d3"];

function gql(query, variables, jar, token) {
  const headers = { "Content-Type": "application/json" };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  const res = http.post(GQL, JSON.stringify({ query, variables }), { headers, jar });
  return res;
}

export default function () {
  const jar = http.cookieJar();

  // 1. Sign in as a guest — one call, no signup wall.
  let res = gql(`mutation { loginAsGuest { token user { id } } }`, {}, jar);
  const ok = check(res, { "signed in": (r) => r.status === 200 && r.json("data.loginAsGuest.token") });
  if (!ok) {
    gameErrors.add(1);
    return;
  }
  const token = res.json("data.loginAsGuest.token");

  // 2. Create a game against the engine.
  res = gql(
    `mutation($in: CreateGameInput!){ createGame(input:$in){ id } }`,
    { in: { vsEngine: true, engineDepth: 6 } },
    jar, token,
  );
  const gameId = res.json("data.createGame.id");
  if (!check(res, { "game created": () => !!gameId })) {
    gameErrors.add(1);
    return;
  }

  // 3. Play the opening. Each move triggers an engine reply, so this is the
  //    stack's real work.
  for (const uci of OPENING) {
    const t0 = Date.now();
    res = gql(
      `mutation($in: MoveInput!){ move(input:$in){ status } }`,
      { in: { gameId, uci } },
      jar, token,
    );
    moveLatency.add(Date.now() - t0);
    const moved = check(res, { "move accepted": (r) => r.status === 200 && !r.json("errors") });
    if (!moved) {
      gameErrors.add(1);
      return;
    }
    // Human think time, so we are not a pathological tight loop.
    sleep(0.3);
  }

  // 4. Resign to finish the game (drives ratings, analysis, PGN archival).
  res = gql(`mutation($in: ResignInput!){ resign(input:$in){ status } }`, { in: { gameId } }, jar, token);
  if (check(res, { "resigned": (r) => r.status === 200 })) {
    gamesCompleted.add(1);
  }
  gameErrors.add(0);

  sleep(1);
}

export function handleSummary(data) {
  const c = (name) => data.metrics[name]?.values;
  const line = (s) => `${s}\n`;
  let out = "\n=== Full game flow under load ===\n";
  out += line(`games completed:   ${c("games_completed")?.count ?? 0}`);
  out += line(`move p95 latency:  ${(c("move_latency_ms")?.["p(95)"] ?? 0).toFixed(0)}ms`);
  out += line(`move avg latency:  ${(c("move_latency_ms")?.avg ?? 0).toFixed(0)}ms`);
  out += line(`http reqs:         ${(c("http_reqs")?.count ?? 0)} (${(c("http_reqs")?.rate ?? 0).toFixed(0)}/s)`);
  out += line(`game error rate:   ${((c("game_errors")?.rate ?? 0) * 100).toFixed(2)}%`);
  out += line(`http failure rate: ${((c("http_req_failed")?.rate ?? 0) * 100).toFixed(2)}%`);
  return { stdout: out };
}
