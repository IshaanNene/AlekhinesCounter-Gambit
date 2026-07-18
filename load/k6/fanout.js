// k6 WebSocket load test for the dedicated spectator fanout tier (services/fanout).
//
// Unlike spectate.js (which drives the gateway's graphql-ws subscription), this
// hammers the purpose-built /spectate endpoint: it opens many plain-WebSocket
// spectators on a single game and measures how fast each one is caught up
// (backlog delivered, "synced" received) and whether connections stay open. This
// is the "one hot game, a huge crowd" shape — the thing the fanout tier exists
// for, where one Redis reader serves every viewer.
//
//   # seed a game first (game-service publishes moves as it is played), then:
//   k6 run -e GAME=<id> -e VUS=2000 -e DURATION=1m load/k6/fanout.js
//   k6 run -e GAME=<id> -e WS=ws://localhost:8090 load/k6/fanout.js   # direct to fanout
//
// GAME is required and must already exist on the event stream. WS defaults to the
// same-origin NGINX route; point it straight at the fanout for an isolated test.

import ws from "k6/ws";
import { check } from "k6";
import { Trend, Counter } from "k6/metrics";

const WS_BASE = __ENV.WS || "ws://localhost:3000";
const GAME = __ENV.GAME || "demo";
const HOLD_MS = Number(__ENV.HOLD || 10) * 1000;

const timeToSynced = new Trend("time_to_synced_ms", true);
const moves = new Counter("moves_received");
const syncs = new Counter("synced");

export const options = {
  scenarios: {
    spectators: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "15s", target: Number(__ENV.VUS || 500) },
        { duration: __ENV.DURATION || "30s", target: Number(__ENV.VUS || 500) },
        { duration: "5s", target: 0 },
      ],
    },
  },
  thresholds: {
    // A joining spectator should be caught up fast even under a large crowd...
    time_to_synced_ms: ["p(95)<2000"],
    // ...and the handshake itself should not stall.
    ws_connecting: ["p(95)<1000"],
    ws_session_duration: ["p(95)>1000"], // connections actually stay open
  },
};

export default function () {
  const start = Date.now();
  const url = `${WS_BASE}/spectate?game=${encodeURIComponent(GAME)}`;

  const res = ws.connect(url, {}, (socket) => {
    socket.on("message", (raw) => {
      let m;
      try { m = JSON.parse(raw); } catch { return; }
      if (m.type === "synced") {
        timeToSynced.add(Date.now() - start);
        syncs.add(1);
      } else if (m.type === "move") {
        moves.add(1);
      }
    });
    // Watch for a while like a real spectator, then leave.
    socket.setTimeout(() => socket.close(), HOLD_MS);
  });

  check(res, { "ws handshake 101": (r) => r && r.status === 101 });
}

export function handleSummary(data) {
  const v = (name) => data.metrics[name]?.values ?? {};
  const conns = v("ws_sessions").count ?? 0;
  let out = "\n=== Fanout tier: spectators on one game ===\n";
  out += `spectators connected: ${conns}\n`;
  out += `caught up (synced):   ${v("synced").count ?? 0}\n`;
  out += `time to synced p95:   ${(v("time_to_synced_ms")["p(95)"] ?? 0).toFixed(0)}ms\n`;
  out += `moves delivered:      ${v("moves_received").count ?? 0}\n`;
  out += `connect p95:          ${(v("ws_connecting")["p(95)"] ?? 0).toFixed(0)}ms\n`;
  out += `handshake failures:   ${((v("ws_connect_errors").rate ?? 0) * 100).toFixed(2)}%\n`;
  return { stdout: out };
}
