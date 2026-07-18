// Standalone spectator view. Connects to the fanout tier's /spectate WebSocket
// for a game and renders the live board from move deltas. Read-only: it reuses
// chess.js purely for presentation and never talks to the play API.
//
// URL: watch.html?game=<id>[&ws=ws://host:port]
//   game  the game to watch
//   ws    optional fanout origin override (for local testing without NGINX);
//         defaults to same-origin, where NGINX proxies /spectate to fanout.
import {
  parseFEN, boardOrder, isLightSquare, glyphFor, squareName, parseUCI,
  fileOf, rankOf, START_FEN,
} from "./chess.js";

const params = new URLSearchParams(location.search);
const gameId = params.get("game");
const wsOverride = params.get("ws");

const boardEl = document.getElementById("board");
const connEl = document.getElementById("conn");
const statusEl = document.getElementById("status");
const movesEl = document.getElementById("moves");

const state = { fen: START_FEN, moves: [], status: "IN_PROGRESS", endReason: "", lastId: "" };

if (!gameId) {
  statusEl.textContent = "No game id — open as watch.html?game=<id>.";
} else {
  render();
  connect();
}

function setConn(text, cls) {
  connEl.textContent = text;
  connEl.className = "pill " + cls;
}

function render() {
  const { squares, turn } = parseFEN(state.fen);
  const last = state.moves.length ? parseUCI(state.moves[state.moves.length - 1].uci) : null;

  boardEl.replaceChildren();
  for (const sq of boardOrder(false)) {
    const piece = squares[sq];
    const cell = document.createElement("div");
    cell.className = "sq" + (isLightSquare(sq) ? "" : " sq--dark");
    cell.setAttribute("role", "gridcell");
    cell.setAttribute("aria-label",
      piece ? `${squareName(sq)}, ${piece.color === "w" ? "white" : "black"} ${piece.type}` : squareName(sq));
    if (last && (sq === last.from || sq === last.to)) cell.classList.add("sq--last");

    if (piece) {
      const span = document.createElement("span");
      span.className = `piece piece--${piece.color}`;
      span.textContent = glyphFor(piece);
      cell.append(span);
    }
    if (rankOf(sq) === 0) cell.append(coord("file", "abcdefgh"[fileOf(sq)]));
    if (fileOf(sq) === 0) cell.append(coord("rank", String(rankOf(sq) + 1)));
    boardEl.append(cell);
  }

  statusEl.textContent = state.status !== "IN_PROGRESS"
    ? statusText(state.status, state.endReason)
    : `${turn === "w" ? "White" : "Black"} to move · ${state.moves.length} ${state.moves.length === 1 ? "ply" : "plies"}`;

  renderMoves();
}

function coord(kind, text) {
  const el = document.createElement("span");
  el.className = `sq__coord sq__coord--${kind}`;
  el.textContent = text;
  return el;
}

function renderMoves() {
  movesEl.replaceChildren();
  for (let i = 0; i < state.moves.length; i += 2) {
    const li = document.createElement("li");
    const num = i / 2 + 1;
    const white = state.moves[i]?.uci ?? "";
    const black = state.moves[i + 1]?.uci ?? "";
    li.textContent = `${num}. ${white}${black ? " " + black : ""}`;
    movesEl.append(li);
  }
}

function statusText(status, reason) {
  const who = status === "WHITE_WON" ? "White wins"
    : status === "BLACK_WON" ? "Black wins" : "Draw";
  return reason ? `${who} — ${reason.toLowerCase()}` : who;
}

let backoff = 500;

function connect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const base = wsOverride || `${proto}//${location.host}`;
  const resume = state.lastId ? `&from=${encodeURIComponent(state.lastId)}` : "";
  const url = `${base}/spectate?game=${encodeURIComponent(gameId)}${resume}`;

  setConn("connecting…", "pill--idle");
  const ws = new WebSocket(url);

  ws.onopen = () => { backoff = 500; };
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg.type === "synced") {
      setConn("live", "pill--live");
      return;
    }
    if (msg.type === "move") {
      // Deltas after our last id, so appending never double-counts on reconnect.
      state.fen = msg.fen || state.fen;
      state.status = msg.status || state.status;
      state.endReason = msg.endReason || state.endReason;
      state.lastId = msg.id || state.lastId;
      state.moves.push({ ply: msg.ply, uci: msg.uci });
      render();
    }
  };
  ws.onclose = () => {
    setConn("reconnecting…", "pill--idle");
    setTimeout(connect, backoff);
    backoff = Math.min(backoff * 2, 10000);
  };
  ws.onerror = () => { try { ws.close(); } catch { /* onclose handles retry */ } };
}
