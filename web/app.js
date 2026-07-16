// Alekhine's Counter-Gambit — web client.
//
// State flows one way: the server is the only source of truth. Every mutation
// returns the new game, and the subscription pushes the same shape, so both
// paths funnel into a single render(). Two deliberate exceptions, both display
// only and both resynced by the next push:
//   * the clock ticks locally between updates;
//   * legal moves are fetched from the server, never computed here.

import { gql, GraphQLError, Subscriber } from "./graphql.js";
import * as settings from "./settings.js";
import { mountSettings } from "./settings-ui.js";
import { mountAccount, refreshMe, ensureSignedIn, onAccountChange, me } from "./account.js";
import { watchAnalysis, verdictFor, QUALITY_ICON } from "./analysis.js";
import {
  parseFEN, parseUCI, squareName, glyphFor, boardOrder,
  isLightSquare, needsPromotion, formatClock, fileOf, rankOf,
} from "./chess.js";

/* ── GraphQL documents ──────────────────────────────────────────────────── */

const GAME_FIELDS = `
  id fen status endReason vsEngine awaitingOpponent whiteId blackId
  moves { ply uci }
  clock { whiteMs blackMs turn running }
`;

const CREATE_GAME = `mutation($in: CreateGameInput!) { createGame(input: $in) { ${GAME_FIELDS} } }`;
const MOVE = `mutation($in: MoveInput!) { move(input: $in) { ${GAME_FIELDS} } }`;
const RESIGN = `mutation($in: ResignInput!) { resign(input: $in) { ${GAME_FIELDS} } }`;
const JOIN = `mutation($id: ID!) { joinGame(gameId: $id) { ${GAME_FIELDS} } }`;
const GET_GAME = `query($id: ID!) { game(id: $id) { ${GAME_FIELDS} } }`;
const LEGAL = `query($id: ID!) { legalMoves(gameId: $id) }`;
const ON_GAME = `subscription($id: ID!) { gameUpdated(gameId: $id) { ${GAME_FIELDS} } }`;

/* ── State ──────────────────────────────────────────────────────────────── */

const state = {
  game: null,
  board: null,       // parsed FEN
  selected: null,    // square index
  side: "s",         // "w" | "b" | "s" (spectator)
  flipped: false,
  clock: null,       // { whiteMs, blackMs, turn, running, at }
  legal: [],         // legal moves (UCI) for the current position, from the server
  premove: null,     // { from, to } queued during the opponent's turn
  pendingMove: null, // { from, to, promotion } awaiting confirmation
  dragFrom: null,    // square a drag started on
  lowTimeFired: false,
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

/* ── Audio (low-time warning) ───────────────────────────────────────────── */

// Created lazily on first interaction: browsers refuse to start an AudioContext
// without a user gesture.
let audioCtx = null;
function beep() {
  try {
    audioCtx ??= new (window.AudioContext ?? window.webkitAudioContext)();
    const osc = audioCtx.createOscillator();
    const gain = audioCtx.createGain();
    osc.type = "sine";
    osc.frequency.value = 880;
    gain.gain.setValueAtTime(0.0001, audioCtx.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.2, audioCtx.currentTime + 0.01);
    gain.gain.exponentialRampToValueAtTime(0.0001, audioCtx.currentTime + 0.35);
    osc.connect(gain).connect(audioCtx.destination);
    osc.start();
    osc.stop(audioCtx.currentTime + 0.36);
  } catch {
    // Audio is a nicety; the visual warning still fires.
  }
}

/* ── Identity ───────────────────────────────────────────────────────────── */

/**
 * Which seat the signed-in user holds in this game. Derived from the session —
 * never chosen — so the board can only ever act as who you actually are.
 */
function sideFor(game, user) {
  if (!game || !user) return "s";
  if (game.whiteId === user.id) return "w";
  if (game.blackId === user.id) return "b";
  return "s";
}

/* ── Settings application ───────────────────────────────────────────────── */

/** Push preference values into the DOM. Called on load and on every change. */
function applySettings() {
  $("board").style.setProperty("--piece-scale", settings.get("pieceSize") / 100);

  const well = document.querySelector(".board-well");
  well.classList.remove("coords-off", "coords-inside", "coords-outside");
  well.classList.add(`coords-${settings.get("coordinates")}`);

  document.querySelector(".app").classList.toggle("focus", settings.get("focusMode"));

  // "White always on bottom" overrides the automatic flip for black.
  if (settings.get("whiteOnBottom")) state.flipped = false;
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

  const lastMove = settings.get("highlightLastMove") && state.game?.moves?.length
    ? parseUCI(state.game.moves[state.game.moves.length - 1].uci)
    : null;

  // Destinations for the selected piece, straight from the server's move list.
  const targets = settings.get("showLegalMoves") && state.selected !== null
    ? legalTargetsFrom(state.selected)
    : new Set();

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
    if (targets.has(sq)) {
      cell.classList.add("sq--legal");
      if (piece) cell.classList.add("sq--capture");
    }
    if (state.premove && (sq === state.premove.from || sq === state.premove.to)) {
      cell.classList.add("sq--premove");
    }
    if (state.dragFrom === sq) cell.classList.add("sq--dragging");

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

    cell.addEventListener("click", (e) => onSquareClick(sq, e));
    cell.addEventListener("pointerdown", (e) => onPointerDown(e, sq));
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
  const lowMs = settings.get("lowTimeSeconds") * 1000;

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
    box.classList.toggle("is-low", settings.get("lowTimeWarning") && ms < lowMs);
  }

  // Audible warning: once per game, and only for the side you are playing.
  if (c?.running && settings.get("lowTimeWarning") && state.side !== "s") {
    const mine = liveMs(state.side);
    if (mine < lowMs && !state.lowTimeFired) {
      state.lowTimeFired = true;
      beep();
    } else if (mine >= lowMs) {
      state.lowTimeFired = false; // re-arm once the increment lifts you clear
    }
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
  for (let i = 0; i < moves.length; i += 2) {
    list.append(cell("moves__no", `${i / 2 + 1}.`));
    list.append(cell("moves__ply", moves[i].uci, i === moves.length - 1, moves[i].ply));
    list.append(moves[i + 1]
      ? cell("moves__ply", moves[i + 1].uci, i + 1 === moves.length - 1, moves[i + 1].ply)
      : cell("moves__ply", ""));
  }
  list.scrollTop = list.scrollHeight;
}

function cell(className, text, isLast = false, ply = null) {
  const li = document.createElement("li");
  li.className = className + (isLast ? " is-last" : "");
  li.textContent = text;

  // Annotate with the engine's verdict once the report has arrived.
  if (ply !== null && text && settings.get("moveClassification")) {
    const v = verdictFor(ply);
    const icon = v ? QUALITY_ICON[v.quality] : "";
    if (icon) {
      const mark = document.createElement("span");
      mark.className = `q q--${v.quality}`;
      mark.textContent = icon;
      mark.title = `${v.quality.toLowerCase()} — engine played ${v.bestUci}` +
        (v.centipawnLoss > 0 ? ` (cost ${v.centipawnLoss}cp)` : "");
      li.append(mark);
    }
  }
  return li;
}

function renderControls() {
  const g = state.game;
  $("share-block").hidden = !g;
  $("side-block").hidden = !g;
  // Offer the open seat only to someone who could actually take it.
  $("join-block").hidden = !(g && g.awaitingOpponent && state.side === "s" && me && g.whiteId !== me.id);

  if (g) {
    $("share-link").value = `${location.origin}${location.pathname}#${g.id}`;
    $("resign").disabled =
      g.status !== "IN_PROGRESS" || state.side === "s" || g.awaitingOpponent;
    $("role").textContent =
      state.side === "w" ? "White" :
      state.side === "b" ? "Black" :
      g.awaitingOpponent ? "Spectator — seat open" : "Spectator";
  }
}

/* ── Legal moves ────────────────────────────────────────────────────────── */

/** Squares the selected piece may move to, per the server's move list. */
function legalTargetsFrom(from) {
  const out = new Set();
  for (const uci of state.legal) {
    const m = parseUCI(uci);
    if (m.from === from) out.add(m.to);
  }
  return out;
}

const isLegal = (from, to) =>
  state.legal.some((uci) => {
    const m = parseUCI(uci);
    return m.from === from && m.to === to;
  });

/**
 * Refresh the legal-move list for the current position. Only fetched when a
 * feature needs it, so a plain spectator costs no extra round trip.
 */
async function refreshLegal() {
  const needed = settings.get("showLegalMoves") || settings.get("premoves");
  if (!needed || !state.game || state.game.status !== "IN_PROGRESS") {
    state.legal = [];
    return;
  }
  try {
    const data = await gql(LEGAL, { id: state.game.id });
    state.legal = data.legalMoves ?? [];
  } catch {
    state.legal = []; // hints are optional; the server still referees
  }
}

/* ── Game updates ───────────────────────────────────────────────────────── */

function applyGame(game) {
  if (!game) return;
  const prevTurn = state.board?.turn;
  const isNewGame = game.id !== state.game?.id;
  state.game = game;
  if (isNewGame) syncSide();
  else state.side = sideFor(game, me); // a join can change our seat mid-stream
  state.board = parseFEN(game.fen);
  state.clock = game.clock ? { ...game.clock, at: Date.now() } : null;
  if (prevTurn !== state.board.turn) state.selected = null;
  render();

  refreshLegal().then(() => {
    renderBoard();
    playPremoveIfReady();
  });

  // The report is produced asynchronously after the game ends, so this polls
  // and re-renders the move list once the verdicts land.
  if (settings.get("engineEval")) {
    watchAnalysis(game, { onReport: () => renderMoves() });
  }
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

/* ── Turn helpers ───────────────────────────────────────────────────────── */

const myTurn = () =>
  Boolean(state.game) &&
  state.game.status === "IN_PROGRESS" &&
  !state.game.awaitingOpponent &&
  state.side !== "s" &&
  state.board?.turn === state.side;

/* ── Interaction: click ─────────────────────────────────────────────────── */

function onSquareClick(sq, event) {
  if (settings.get("moveMethod") === "drag") return; // drag-only: ignore clicks
  handleSelect(sq, event?.altKey ?? false);
}

function handleSelect(sq, altKey = false) {
  const g = state.game;
  if (!g || g.status !== "IN_PROGRESS" || !state.board) return;
  if (state.side === "s") {
    toast("You are spectating — pick a side to play.");
    return;
  }

  const piece = state.board.squares[sq];

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

  const from = state.selected;
  state.selected = null;
  attemptMove(from, sq, altKey);
}

/* ── Interaction: drag ──────────────────────────────────────────────────── */

let ghost = null;

function onPointerDown(e, sq) {
  const method = settings.get("moveMethod");
  if (method === "click") return;
  if (e.button !== 0) return;

  const g = state.game;
  if (!g || g.status !== "IN_PROGRESS" || state.side === "s") return;
  const piece = state.board?.squares[sq];
  if (!piece || piece.color !== state.side) return;

  state.dragFrom = sq;
  state.selected = sq; // so legal-move dots show while dragging
  renderBoard();

  ghost = document.createElement("div");
  ghost.className = `drag-ghost piece piece--${piece.color}`;
  ghost.style.fontSize = getComputedStyle(document.querySelector(".sq")).fontSize;
  ghost.textContent = glyphFor(piece);
  moveGhost(ghost, e.clientX, e.clientY);
  document.body.append(ghost);
  $("board").classList.add("is-dragging");

  // Pointer capture would pin events to the origin square, but we need to know
  // which square is under the cursor on release, so track on document instead.
  document.addEventListener("pointermove", onPointerMove);
  document.addEventListener("pointerup", onPointerUp, { once: true });
  e.preventDefault();
}

const moveGhost = (el, x, y) => {
  el.style.left = `${x}px`;
  el.style.top = `${y}px`;
};

function onPointerMove(e) {
  if (ghost) moveGhost(ghost, e.clientX, e.clientY);
}

function onPointerUp(e) {
  document.removeEventListener("pointermove", onPointerMove);
  ghost?.remove();
  ghost = null;
  $("board").classList.remove("is-dragging");

  const from = state.dragFrom;
  state.dragFrom = null;
  if (from === null) return;

  const target = document.elementFromPoint(e.clientX, e.clientY)?.closest(".sq");
  const to = target ? Number(target.dataset.sq) : null;

  // Released on the origin (or off-board): fall back to click-to-select so a
  // drag that never moved behaves like a plain click.
  if (to === null || to === from) {
    renderBoard();
    return;
  }
  state.selected = null;
  attemptMove(from, to, e.altKey);
}

/* ── Move pipeline ──────────────────────────────────────────────────────── */

/**
 * The single funnel for an attempted move: premove queueing, promotion choice,
 * and confirmation all happen here so click and drag behave identically.
 */
async function attemptMove(from, to, altKey = false) {
  // Not your turn: queue as a premove if enabled, otherwise reject early.
  if (!myTurn()) {
    if (!settings.get("premoves")) {
      toast("Not your turn.");
      renderBoard();
      return;
    }
    state.premove = { from, to };
    renderBoard();
    toast(`Premove ${squareName(from)}${squareName(to)} queued.`);
    return;
  }

  const promotion = await resolvePromotion(from, to, altKey);
  if (promotion === false) { renderBoard(); return; } // picker dismissed

  if (settings.get("confirmMove")) {
    state.pendingMove = { from, to, promotion };
    showConfirmBar(from, to, promotion);
    renderBoard();
    return;
  }
  await submitMove(from, to, promotion);
}

/**
 * Decide the promotion piece. Returns a letter, null when not a promotion, or
 * false if the user dismissed the picker.
 *
 * Auto-queen is the default; holding ALT opens the picker instead. With
 * auto-queen off, the picker always opens.
 */
async function resolvePromotion(from, to, altKey) {
  if (!state.board || !needsPromotion(state.board.squares, from, to)) return null;
  const auto = settings.get("autoQueen") && !altKey;
  return auto ? "q" : askPromotion();
}

function askPromotion() {
  const scrim = $("promo-scrim");
  const box = $("promo-choices");
  box.replaceChildren();

  return new Promise((resolve) => {
    const finish = (value) => {
      scrim.hidden = true;
      document.removeEventListener("keydown", onKey);
      resolve(value);
    };
    const onKey = (e) => { if (e.key === "Escape") finish(false); };

    for (const [type, label] of [["q", "Queen"], ["r", "Rook"], ["b", "Bishop"], ["n", "Knight"]]) {
      const btn = document.createElement("button");
      btn.className = `promo__btn piece--${state.side}`;
      btn.textContent = glyphFor({ type });
      btn.title = label;
      btn.setAttribute("aria-label", label);
      btn.addEventListener("click", () => finish(type));
      box.append(btn);
    }
    scrim.hidden = false;
    document.addEventListener("keydown", onKey);
    box.firstChild?.focus();
    // Clicking the backdrop cancels.
    scrim.onclick = (e) => { if (e.target === scrim) finish(false); };
  });
}

function showConfirmBar(from, to, promotion) {
  $("confirm-move-text").textContent = squareName(from) + squareName(to) + (promotion ?? "");
  $("confirm-bar").hidden = false;
}

function hideConfirmBar() {
  $("confirm-bar").hidden = true;
  state.pendingMove = null;
}

async function submitMove(from, to, promotion) {
  const uci = squareName(from) + squareName(to) + (promotion ?? "");
  state.selected = null;
  hideConfirmBar();
  renderBoard();

  try {
    const data = await gql(MOVE, { in: { gameId: state.game.id, uci } });
    applyGame(data.move);
  } catch (err) {
    // The server is the referee: surface its verdict rather than guessing.
    toast(friendlyError(err), true);
    renderBoard();
  }
}

/**
 * Play a queued premove once it becomes our turn. Screened against the server's
 * legal-move list first, so an premove invalidated by the opponent's reply is
 * discarded quietly instead of bouncing off the API.
 */
function playPremoveIfReady() {
  const pm = state.premove;
  if (!pm || !myTurn()) return;
  state.premove = null;

  if (state.legal.length && !isLegal(pm.from, pm.to)) {
    renderBoard();
    toast("Premove is no longer legal — discarded.");
    return;
  }
  const promotion = needsPromotion(state.board.squares, pm.from, pm.to) ? "q" : null;
  submitMove(pm.from, pm.to, promotion);
}

/* ── Confirmation dialog ────────────────────────────────────────────────── */

/** Promise-based confirm. Used for resign, and for draw offers once they exist. */
function confirmDialog(title, body, confirmLabel = "Confirm") {
  const scrim = $("confirm-scrim");
  $("confirm-title").textContent = title;
  $("confirm-body").textContent = body;
  $("confirm-yes").textContent = confirmLabel;

  return new Promise((resolve) => {
    const finish = (value) => {
      scrim.hidden = true;
      $("confirm-yes").onclick = null;
      $("confirm-no").onclick = null;
      document.removeEventListener("keydown", onKey);
      resolve(value);
    };
    const onKey = (e) => { if (e.key === "Escape") finish(false); };

    $("confirm-yes").onclick = () => finish(true);
    $("confirm-no").onclick = () => finish(false);
    scrim.onclick = (e) => { if (e.target === scrim) finish(false); };
    document.addEventListener("keydown", onKey);
    scrim.hidden = false;
    $("confirm-no").focus();
  });
}

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

const timeControl = () => ({
  initialMs: Math.max(1, Number($("minutes").value || 5)) * 60_000,
  incrementMs: Math.max(0, Number($("increment").value || 0)) * 1000,
});

function startGame(game) {
  state.premove = null;
  state.lowTimeFired = false;
  state.game = null; // force a fresh seat computation
  applyGame(game);
  location.hash = game.id;
  subscriber.subscribe(ON_GAME, { id: game.id }, (p) => applyGame(p?.gameUpdated));
}

async function init() {
  mountSettings({ openButton: $("open-settings"), drawer: $("settings-drawer") });
  mountAccount({ toast, friendlyError, onOpenGame: (id) => openGame(id) });
  applySettings();

  // Signing in or out changes which seat we hold, so re-render the board.
  onAccountChange(() => {
    if (state.game) state.side = sideFor(state.game, me);
    render();
  });
  await refreshMe();

  // Re-apply and re-render on any preference change, so toggles take effect
  // immediately rather than on the next move.
  settings.onChange(() => {
    applySettings();
    refreshLegal().then(render);
  });

  $("depth").addEventListener("input", (e) => { $("depth-out").value = e.target.value; });

  press($("new-engine"), async () => {
    // Signing in as a guest is one call, so "play now" never hits a signup wall.
    await ensureSignedIn();
    const data = await gql(CREATE_GAME, {
      in: { vsEngine: true, engineDepth: Number($("depth").value) },
    });
    startGame(data.createGame);
    toast("New game against Stockfish.");
  });

  press($("new-human"), async () => {
    await ensureSignedIn();
    const { initialMs, incrementMs } = timeControl();
    // Leave Black open: whoever opens the link claims the seat as themselves.
    const data = await gql(CREATE_GAME, {
      in: { vsEngine: false, initialMs, incrementMs, rated: true },
    });
    startGame(data.createGame);
    toast("Game created — send the link to your opponent.");
  });

  press($("join-game"), async () => {
    await ensureSignedIn();
    const data = await gql(JOIN, { id: state.game.id });
    applyGame(data.joinGame);
    toast("You are playing Black.");
  });

  press($("copy-link"), async () => {
    await navigator.clipboard.writeText($("share-link").value);
    toast("Link copied.");
  });

  press($("resign"), async () => {
    if (settings.get("confirmResign")) {
      const ok = await confirmDialog(
        "Resign this game?",
        "Your opponent will be awarded the win. This cannot be undone.",
        "Resign",
      );
      if (!ok) return;
    }
    const data = await gql(RESIGN, { in: { gameId: state.game.id } });
    applyGame(data.resign);
  });

  $("confirm-cancel").addEventListener("click", () => { hideConfirmBar(); renderBoard(); });
  $("confirm-play").addEventListener("click", () => {
    const pm = state.pendingMove;
    if (pm) submitMove(pm.from, pm.to, pm.promotion);
  });

  $("flip").addEventListener("click", () => {
    state.flipped = !state.flipped;
    renderBoard();
  });

  // Tick the clock display ~4x/second; authoritative values arrive on push.
  setInterval(() => { if (state.clock?.running) renderClocks(); }, 250);

  // startGame writes location.hash itself, so guard against re-opening (and
  // re-subscribing to) the game we just started.
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
  state.premove = null;
}

/** Recompute our seat and orientation from the session. */
function syncSide() {
  const side = sideFor(state.game, me);
  setSide(side);
  state.flipped = !settings.get("whiteOnBottom") && side === "b";
}

init();
