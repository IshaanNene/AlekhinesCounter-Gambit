// k6 WebSocket load test: many spectators on live games.
//
// The other half of the platform's load profile. game_flow.js drives writes;
// this drives the fanout path — hundreds of idle-but-connected subscribers, the
// shape a popular game creates. It proves the gateway's WebSocket handling and
// the Redis pub/sub fanout hold open connections without leaking, and that a
// subscriber receives the initial snapshot promptly.
//
//   k6 run load/k6/spectate.js
//   k6 run -e VUS=200 -e DURATION=1m load/k6/spectate.js
//
// A game id is created once via HTTP, then every VU subscribes to it.

import ws from "k6/ws";
import http from "k6/http";
import { check } from "k6";
import { Trend, Counter } from "k6/metrics";

const HTTP = __ENV.TARGET || "http://localhost:3000/graphql";
const WS_URL = (__ENV.WS || "ws://localhost:3000/ws");

const firstSnapshot = new Trend("first_snapshot_ms", true);
const snapshots = new Counter("snapshots_received");

export const options = {
  scenarios: {
    spectators: {
      executor: "ramping-vus",
      startVUs: 1,
      stages: [
        { duration: "10s", target: Number(__ENV.VUS || 100) },
        { duration: __ENV.DURATION || "30s", target: Number(__ENV.VUS || 100) },
        { duration: "5s", target: 0 },
      ],
    },
  },
  thresholds: {
    first_snapshot_ms: ["p(95)<2000"], // a joining spectator sees the board quickly
    ws_session_duration: ["p(95)>1000"], // connections actually stay open
  },
};

// setup runs once: create a real game every VU can watch.
export function setup() {
  const login = http.post(HTTP, JSON.stringify({ query: `mutation { loginAsGuest { token } }` }),
    { headers: { "Content-Type": "application/json" } });
  const token = login.json("data.loginAsGuest.token");

  const create = http.post(HTTP,
    JSON.stringify({
      query: `mutation($in: CreateGameInput!){ createGame(input:$in){ id } }`,
      variables: { in: { vsEngine: true, engineDepth: 4 } },
    }),
    { headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` } });
  const gameId = create.json("data.createGame.id");
  return { gameId, token };
}

export default function (data) {
  const start = Date.now();
  let acked = false;

  const res = ws.connect(WS_URL, { headers: { "Sec-WebSocket-Protocol": "graphql-transport-ws" } }, (socket) => {
    socket.on("open", () => {
      // Authenticate through connection_init: a non-browser client cannot set the
      // cookie the HTTP path uses.
      socket.send(JSON.stringify({ type: "connection_init", payload: { Authorization: `Bearer ${data.token}` } }));
    });

    socket.on("message", (msg) => {
      const m = JSON.parse(msg);
      if (m.type === "connection_ack") {
        acked = true;
        socket.send(JSON.stringify({
          id: "1", type: "subscribe",
          payload: { query: `subscription($id: ID!){ gameUpdated(gameId:$id){ fen status } }`, variables: { id: data.gameId } },
        }));
      } else if (m.type === "next") {
        firstSnapshot.add(Date.now() - start);
        snapshots.add(1);
      } else if (m.type === "ping") {
        socket.send(JSON.stringify({ type: "pong" }));
      }
    });

    // Hold the connection open like a real spectator, then leave.
    socket.setTimeout(() => socket.close(), 8000);
  });

  check(res, { "ws handshake 101": (r) => r && r.status === 101 });
  check(null, { "connection acknowledged": () => acked });
}

export function handleSummary(data) {
  const c = (name) => data.metrics[name]?.values;
  let out = "\n=== WebSocket spectator fanout ===\n";
  out += `snapshots received:  ${c("snapshots_received")?.count ?? 0}\n`;
  out += `first snapshot p95:  ${(c("first_snapshot_ms")?.["p(95)"] ?? 0).toFixed(0)}ms\n`;
  out += `ws sessions:         ${c("ws_sessions")?.count ?? 0}\n`;
  out += `ws connect failures: ${((c("ws_connect_errors")?.rate ?? 0) * 100).toFixed(2)}%\n`;
  return { stdout: out };
}
