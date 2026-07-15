// Alekhine's Counter-Gambit — web client.
//
// State flows one way: the server is the only source of truth. Every mutation
// returns the new game, and the subscription pushes the same shape, so both
// paths funnel into a single render(). The one exception is the clock, which
// ticks locally between pushes purely for display and resyncs on every update.

import { gql, GraphQLError, Subscriber } from "./graphql.js";
import {
  parseFEN, parseUCI, squareName, glyphFor, boardOrder,
  isLightSquare, needsPromotion, formatClock, fileOf, rankOf,
} from "./chess.js";

/* ── GraphQL documents ──────────────────────────────────────────────────── */

const GAME_FIELDS = `
  id fen status endReason vsEngine whiteId blackId
  moves { ply uci }
  clock { whiteMs blackMs turn running }
`;

const CREATE_GUEST = `mutation { createGuest { id username } }`;
const CREATE_GAME = `mutation($in: CreateGameInput!) { createGame(input: $in) { ${GAME_FIELDS} } }`;
const MOVE = `mutation($in: MoveInput!) { move(input: $in) { ${GAME_FIELDS} } }`;
const RESIGN = `mutation($in: ResignInput!) { resign(input: $in) { ${GAME_FIELDS} } }`;
const GET_GAME = `query($id: ID!) { game(id: $id) { ${GAME_FIELDS} } }`;
const ON_GAME = `subscription($id: ID!) { gameUpdated(gameId: $id) { ${GAME_FIELDS} } }`;

/* ── State ──────────────────────────────────────────────────────────────── */

const state = {
  game: null,
  board: null,      // parsed FEN
  selected: null,   // square index
  side: "s",        // "w" | "b" | "s" (spectator)
  flipped: false,
  /** Clock as last reported, plus when we received it, for local ticking. */
  clock: null,      // { whiteMs, blackMs, turn, running, at }
};

const $ = (id) => document.getElementById(id);
const subscriber = new Subscriber(onConnectionState);

/* ── Toast ──────────────────────────────────────────────────────────────── */

let toastTimer = null;
function toast(message, isError = false) {
  const el = $("toast");
  el.textContent = message;
  el.classList.toggle("is-error", isError);
  el.classList.add("is-visible");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("is-visible"), 3200);
}

function onConnectionState(kind, detail) {
  const pill = $("conn");
  pill.dataset.state = kind === "live" ? "live" : kind === "error" ? "error" : "offline";
  $("conn-text").textContent =
    kind === "live" ? "live" :
    kind === "connecting" ? "connecting…" :
    kind === "error" ? (detail ?? "error") :
    kind === "idle" ? "no game" : "reconnecting…";
}

/* ── Identity ───────────────────────────────────────────────────────────── */

// A guest id stands in for a real account until auth lands (T2.8). Persisted so
// a refresh keeps your seat at the board.
async function myGuestId() {
  const cached = localStorage.getItem("acg.guestId");
  if (cached) return cached;
  const { createGuest } = await gql(CREATE_GUEST);
  localStorage.setItem("acg.guestId", createGuest.id);
  return createGuest.id;
}

/* ── Rendering ──────────────────────────────────────────────────────────── */

function render() {
  renderBoard();
  renderClocks();
  renderStatus();
  renderMoves();
  renderControls();
}

function renderBoard() {
  const boardEl = $("board");
  const squares = state.board?.squares ?? new Array(64).fill(null);

  const lastMove = state.game?.moves?.length
    ? parseUCI(state.game.moves[state.game.moves.length - 1].uci)
    : null;

  boardEl.replaceChildren();
  for (const sq of boardOrder(state.flipped)) {
    const piece = squares[sq];
    const cell = document.createElement("button");
    cell.type = "button";
    cell.className = "sq" + (isLightSquare(sq) ? "" : " sq--dark");
    cell.dataset.sq = String(sq);
    cell.setAttribute("role", "gridcell");
    cell.setAttribute("aria-label",
      piece ? `${squareName(sq)}, ${piece.color === "w" ? "white" : "black"} ${piece.type}`
            : squareName(sq));

    if (sq === state.selected) cell.classList.add("sq--selected");
    if (lastMove && (sq === lastMove.from || sq === lastMove.to)) cell.classList.add("sq--last");

    if (piece) {
      const span = document.createElement("span");
      span.className = `piece piece--${piece.color}`;
      span.textContent = glyphFor(piece);
      cell.append(span);
    }

    // Coordinates only along the two visible edges, like a real board.
    const onBottomEdge = state.flipped ? rankOf(sq) === 7 : rankOf(sq) === 0;
    const onLeftEdge = state.flipped ? fileOf(sq) === 7 : fileOf(sq) === 0;
    if (onBottomEdge) cell.append(coord("file", "abcdefgh"[fileOf(sq)]));
    if (onLeftEdge) cell.append(coord("rank", String(rankOf(sq) + 1)));

    cell.addEventListener("click", () => onSquareClick(sq));
    boardEl.append(cell);
  }

  const turnNote = $("turn-note");
  if (!state.game) turnNote.textContent = "—";
  else if (state.game.status !== "IN_PROGRESS") turnNote.textContent = "game over";
  else turnNote.textContent = `${state.board.turn === "w" ? "White" : "Black"} to move`;
}

function coord(kind, text) {
  const el = document.createElement("span");
  el.className = `sq__coord sq__coord--${kind}`;
  el.textContent = text;
  return el;
}

function renderClocks() {
  const c = state.clock;
  for (const side of ["w", "b"]) {
    const box = $(`clock-${side}`);
    const timeEl = $(`time-${side}`);
    if (!c) {
      timeEl.textContent = "—";
      box.classList.remove("is-active", "is-low");
      continue;
    }
    const ms = liveMs(side);
    timeEl.textContent = formatClock(ms);
    box.classList.toggle("is-active", c.running && c.turn.toLowerCase()[0] === side);
    box.classList.toggle("is-low", ms < 30000);
  }
}

/**
 * The displayed time for a side: the server value, minus time elapsed since we
 * received it if that side's clock is the one running. Display only — the
 * session-manager remains authoritative and every push resyncs us.
 */
function liveMs(side) {
  const c = state.clock;
  if (!c) return 0;
  const base = side === "w" ? c.whiteMs : c.blackMs;
  const isRunning = c.running && c.turn.toLowerCase()[0] === side;
  return isRunning ? Math.max(0, base - (Date.now() - c.at)) : base;
}

function renderStatus() {
  const el = $("status");
  const g = state.game;
  if (!g) {
    el.textContent = "No game yet — start one on the left.";
    el.classList.remove("is-over");
    return;
  }
  if (g.status === "IN_PROGRESS") {
    el.classList.remove("is-over");
    const who = g.vsEngine ? "You vs Stockfish" : "Two players";
    el.innerHTML = `<strong>${who}</strong> — in progress.`;
    return;
  }
  el.classList.add("is-over");
  const result =
    g.status === "WHITE_WON" ? "White wins" :
    g.status === "BLACK_WON" ? "Black wins" : "Draw";
  const reason = (g.endReason ?? "").toLowerCase().replace(/_/g, " ");
  el.innerHTML = `<strong>${result}</strong>${reason ? ` by ${reason}` : ""}.`;
}

function renderMoves() {
  const list = $("moves");
  list.replaceChildren();
  const moves = state.game?.moves ?? [];
  if (!moves.length) {
    const empty = document.createElement("li");
    empty.className = "moves__empty";
    empty.textContent = "No moves yet.";
    list.append(empty);
    return;
  }
  // Group half-moves into numbered pairs: "1. e2e4 e7e5".
  for (let i = 0; i < moves.length; i += 2) {
    list.append(cell("moves__no", `${i / 2 + 1}.`));
    list.append(cell("moves__ply", moves[i].uci, i === moves.length - 1));
    if (moves[i + 1]) {
      list.append(cell("moves__ply", moves[i + 1].uci, i + 1 === moves.length - 1));
    } else {
      list.append(cell("moves__ply", ""));
    }
  }
  list.scrollTop = list.scrollHeight;
}

function cell(className, text, isLast = false) {
  const li = document.createElement("li");
  li.className = className + (isLast ? " is-last" : "");
  li.textContent = text;
  return li;
}

function renderControls() {
  const g = state.game;
  $("share-block").hidden = !g;
  $("side-block").hidden = !g;
  if (g) {
    $("share-link").value = `${location.origin}${location.pathname}#${g.id}`;
    // Only a participant in a live game can resign.
    $("resign").disabled = g.status !== "IN_PROGRESS" || state.side === "s";
  }
  for (const btn of document.querySelectorAll(".segmented__btn")) {
    btn.classList.toggle("is-active", btn.dataset.side === state.side);
  }
}

/* ── Game updates ───────────────────────────────────────────────────────── */

function applyGame(game) {
  if (!game) return;
  state.game = game;
  state.board = parseFEN(game.fen);
  state.clock = game.clock ? { ...game.clock, at: Date.now() } : null;
  render();
}

async function openGame(id, { subscribe = true } = {}) {
  location.hash = id;
  const data = await gql(GET_GAME, { id });
  if (!data.game) {
    toast("That game does not exist.", true);
    return;
  }
  applyGame(data.game);
  if (subscribe) {
    subscriber.subscribe(ON_GAME, { id }, (payload) => applyGame(payload?.gameUpdated));
  }
}

/* ── Interaction ────────────────────────────────────────────────────────── */

function onSquareClick(sq) {
  const g = state.game;
  if (!g || g.status !== "IN_PROGRESS" || !state.board) return;

  if (state.side === "s") {
    toast("You are spectating — pick a side to play.");
    return;
  }

  const piece = state.board.squares[sq];

  // First click: select one of your own pieces.
  if (state.selected === null) {
    if (!piece) return;
    if (piece.color !== state.side) {
      toast("That is not your piece.");
      return;
    }
    state.selected = sq;
    renderBoard();
    return;
  }

  // Clicking the same square clears; clicking another of your pieces reselects.
  if (sq === state.selected) {
    state.selected = null;
    renderBoard();
    return;
  }
  if (piece && piece.color === state.side) {
    state.selected = sq;
    renderBoard();
    return;
  }

  submitMove(state.selected, sq);
}

async function submitMove(from, to) {
  let uci = squareName(from) + squareName(to);
  if (needsPromotion(state.board.squares, from, to)) uci += "q"; // auto-queen
  state.selected = null;
  renderBoard();

  try {
    const data = await gql(MOVE, {
      in: {
        gameId: state.game.id,
        uci,
        // Engine games take no player id; human games are authorised by it.
        playerId: state.game.vsEngine ? null : playerIdForSide(state.side),
      },
    });
    applyGame(data.move);
  } catch (err) {
    // The server is the referee: surface its verdict rather than guessing.
    toast(friendlyError(err), true);
  }
}

/** Map the chosen side to the game's stored player id. */
function playerIdForSide(side) {
  if (!state.game) return null;
  return side === "w" ? state.game.whiteId : state.game.blackId;
}

/** Strip gRPC framing so users see "illegal move: e2e5", not a stack of codes. */
function friendlyError(err) {
  if (!(err instanceof GraphQLError)) return err.message ?? "Something went wrong.";
  const m = err.message.match(/desc = (.*)$/);
  return m ? m[1] : err.message;
}

/* ── Wiring ─────────────────────────────────────────────────────────────── */

function press(btn, fn) {
  btn.addEventListener("click", async () => {
    btn.classList.add("is-pressed");
    try {
      await fn();
    } catch (err) {
      toast(friendlyError(err), true);
    } finally {
      setTimeout(() => btn.classList.remove("is-pressed"), 140);
    }
  });
}

function timeControl() {
  return {
    initialMs: Math.max(1, Number($("minutes").value || 5)) * 60_000,
    incrementMs: Math.max(0, Number($("increment").value || 0)) * 1000,
  };
}

function init() {
  $("depth").addEventListener("input", (e) => { $("depth-out").value = e.target.value; });

  press($("new-engine"), async () => {
    const me = await myGuestId();
    const data = await gql(CREATE_GAME, {
      in: { whiteId: me, engineDepth: Number($("depth").value) },
    });
    setSide("w");
    state.flipped = false;
    applyGame(data.createGame);
    location.hash = data.createGame.id;
    subscriber.subscribe(ON_GAME, { id: data.createGame.id },
      (p) => applyGame(p?.gameUpdated));
    toast("New game against Stockfish.");
  });

  press($("new-human"), async () => {
    // Two identities so both seats exist; without auth either tab may claim
    // either side, which is exactly what makes the share link demo work.
    const white = await myGuestId();
    const { createGuest: black } = await gql(CREATE_GUEST);
    const { initialMs, incrementMs } = timeControl();
    const data = await gql(CREATE_GAME, {
      in: { whiteId: white, blackId: black.id, initialMs, incrementMs },
    });
    setSide("w");
    state.flipped = false;
    applyGame(data.createGame);
    location.hash = data.createGame.id;
    subscriber.subscribe(ON_GAME, { id: data.createGame.id },
      (p) => applyGame(p?.gameUpdated));
    toast("Game created — share the link to play.");
  });

  press($("copy-link"), async () => {
    await navigator.clipboard.writeText($("share-link").value);
    toast("Link copied.");
  });

  press($("resign"), async () => {
    const data = await gql(RESIGN, {
      in: { gameId: state.game.id, playerId: playerIdForSide(state.side) },
    });
    applyGame(data.resign);
  });

  $("flip").addEventListener("click", () => {
    state.flipped = !state.flipped;
    renderBoard();
  });

  for (const btn of document.querySelectorAll(".segmented__btn")) {
    btn.addEventListener("click", () => {
      setSide(btn.dataset.side);
      if (btn.dataset.side !== "s") state.flipped = btn.dataset.side === "b";
      render();
    });
  }

  // Tick the clock display ~4x/second; authoritative values arrive on push.
  setInterval(() => { if (state.clock?.running) renderClocks(); }, 250);

  // Deep link: /#<gameId> opens straight into a live board.
  window.addEventListener("hashchange", () => {
    const id = location.hash.slice(1);
    if (id && id !== state.game?.id) openGame(id).catch(() => {});
  });

  const initial = location.hash.slice(1);
  if (initial) {
    openGame(initial).catch((err) => toast(friendlyError(err), true));
  } else {
    onConnectionState("idle");
    render();
  }
}

function setSide(side) {
  state.side = side;
  state.selected = null;
}

init();
