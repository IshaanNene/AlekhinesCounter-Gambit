// Post-game analysis view: accuracy, an eval graph, and per-move verdicts.
//
// The report is produced asynchronously, so `gameAnalysis` returning null means
// "not yet" rather than "failed". This polls briefly after a game ends and shows
// its working while it waits — the honest state, rather than an empty panel.

import { gql } from "./graphql.js";

const PGN_URL = `query($id: ID!) { gamePgnUrl(gameId: $id) }`;

const ANALYSIS = `query($id: ID!) {
  gameAnalysis(gameId: $id) {
    depth
    white { accuracy acpl matchRate blunders mistakes inaccuracies }
    black { accuracy acpl matchRate blunders mistakes inaccuracies }
    moves { ply uci bestUci evalBeforeCp centipawnLoss quality matchedEngine }
    noveltyFen
    noveltyPly
  }
}`;

const $ = (id) => document.getElementById(id);

// A tiny toast shim so this module needs no import from app.js.
function toastLike(msg) {
  const el = document.getElementById("toast");
  if (!el) return;
  el.textContent = msg;
  el.classList.add("is-visible");
  setTimeout(() => el.classList.remove("is-visible"), 3000);
}

/** Symbols for the move list, mirroring the engine's verdicts. */
export const QUALITY_ICON = {
  BRILLIANT: "!!",
  BEST: "★",
  EXCELLENT: "",
  GOOD: "",
  INACCURACY: "?!",
  MISTAKE: "?",
  BLUNDER: "??",
};

let pollTimer = null;

// The report for the game on screen, held in a mutable object rather than a
// re-assigned `export let`. Exported bindings are live in the spec, but relying
// on that couples every reader to the exporter's assignment timing; a plain
// object has one identity and no such subtlety.
const held = { report: null };

/** The report for the game on screen, or null. */
export const current = () => held.report;

/** Verdict for a given ply, when a report exists. */
export function verdictFor(ply) {
  return held.report?.moves?.find((m) => m.ply === ply) ?? null;
}

/**
 * Show the analysis for a finished game, polling until the worker produces it.
 * Called with a null/unfinished game to clear.
 */
export function watchAnalysis(game, { onReport } = {}) {
  clearTimeout(pollTimer);

  if (!game || game.status === "IN_PROGRESS") {
    held.report = null;
    $("analysis-block").hidden = true;
    return;
  }

  // Already have this game's report: leave it on screen. applyGame runs again on
  // every subscription push, and re-fetching would blank the panel each time.
  if (held.report?.gameId === game.id) {
    $("analysis-block").hidden = false;
    return;
  }

  held.report = null;
  $("analysis-block").hidden = false;
  renderPending("Analysing the game…");

  let attempts = 0;
  const poll = async () => {
    attempts += 1;
    let data;
    try {
      data = await gql(ANALYSIS, { id: game.id });
    } catch {
      renderPending("Could not load the analysis.");
      return;
    }
    const report = data.gameAnalysis;
    if (report) {
      held.report = { ...report, gameId: game.id };
      render(report);
      onReport?.(held.report);
      return;
    }
    // A long game is many engine evaluations; give it a while before giving up.
    if (attempts > 30) {
      renderPending("Analysis is taking longer than usual — check back shortly.");
      return;
    }
    pollTimer = setTimeout(poll, 2000);
  };
  poll();
}

function renderPending(text) {
  const el = $("analysis");
  el.replaceChildren();
  const p = document.createElement("p");
  p.className = "analysis__pending";
  p.textContent = text;
  el.append(p);
}

function render(a) {
  const el = $("analysis");
  el.replaceChildren();

  // Accuracy is the headline.
  const acc = document.createElement("div");
  acc.className = "acc";
  acc.append(sideCard("White", a.white), sideCard("Black", a.black));
  el.append(acc);

  el.append(evalGraph(a.moves));

  el.append(statRow("Engine match", `${pct(a.white.matchRate)} · ${pct(a.black.matchRate)}`));
  el.append(statRow("Blunders", `${a.white.blunders} · ${a.black.blunders}`));
  el.append(statRow("Mistakes", `${a.white.mistakes} · ${a.black.mistakes}`));
  el.append(statRow("Depth", String(a.depth)));

  // Download the game as PGN, straight from object storage.
  const dl = document.createElement("button");
  dl.className = "btn btn--ghost";
  dl.textContent = "⬇ Download PGN";
  dl.addEventListener("click", async () => {
    try {
      const data = await gql(PGN_URL, { id: held.report.gameId });
      if (data.gamePgnUrl) {
        window.location.href = data.gamePgnUrl; // presigned; browser saves the file
      } else {
        toastLike("The PGN is not ready yet — try again in a moment.");
      }
    } catch {
      toastLike("Could not fetch the PGN.");
    }
  });
  el.append(dl);

  // A theoretical novelty is rare enough to call out.
  if (a.noveltyFen) {
    const n = document.createElement("div");
    n.className = "novelty";
    n.innerHTML = `<strong>Theoretical novelty</strong>
      Move ${Math.floor(a.noveltyPly / 2) + 1} reached a position never seen before on this platform.`;
    el.append(n);
  }
}

function sideCard(who, s) {
  const el = document.createElement("div");
  el.className = "acc__side";
  el.innerHTML = `
    <div class="acc__who">${who}</div>
    <div class="acc__num">${s.accuracy.toFixed(1)}</div>
    <div class="acc__sub">accuracy · ${s.acpl.toFixed(0)} acpl</div>`;
  return el;
}

function statRow(label, value) {
  const el = document.createElement("div");
  el.className = "stat-row";
  el.innerHTML = `<span>${label}</span><strong>${value}</strong>`;
  return el;
}

const pct = (f) => `${Math.round(f * 100)}%`;

/**
 * A sparkline of the evaluation through the game, always from White's view.
 *
 * Engine scores are from the side to move, so odd plies must be negated to put
 * the whole game on one axis — otherwise the line zig-zags every move and shows
 * nothing. Squashed through tanh so a decisive +900 does not flatten the opening
 * into a straight line.
 */
function evalGraph(moves) {
  const W = 240, H = 64;
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "evalgraph");
  svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
  svg.setAttribute("preserveAspectRatio", "none");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "Evaluation through the game, from White's perspective");

  if (!moves.length) return svg;

  const y = (cp) => {
    // tanh keeps the interesting middle readable while still bounding mates.
    const norm = Math.tanh(cp / 400);
    return H / 2 - (norm * H) / 2;
  };
  const pts = moves.map((m, i) => {
    // evalBeforeCp is from the mover's view; negate Black's to face White.
    const whiteView = m.ply % 2 === 1 ? m.evalBeforeCp : -m.evalBeforeCp;
    return [(i / Math.max(1, moves.length - 1)) * W, y(whiteView)];
  });

  const line = pts.map(([x, yy]) => `${x.toFixed(1)},${yy.toFixed(1)}`).join(" ");
  const area = `0,${H / 2} ${line} ${W},${H / 2}`;

  const fill = document.createElementNS("http://www.w3.org/2000/svg", "polygon");
  fill.setAttribute("class", "evalgraph__fill");
  fill.setAttribute("points", area);

  const mid = document.createElementNS("http://www.w3.org/2000/svg", "line");
  mid.setAttribute("class", "evalgraph__mid");
  mid.setAttribute("x1", "0"); mid.setAttribute("x2", String(W));
  mid.setAttribute("y1", String(H / 2)); mid.setAttribute("y2", String(H / 2));

  const path = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
  path.setAttribute("class", "evalgraph__line");
  path.setAttribute("points", line);

  svg.append(fill, mid, path);
  return svg;
}
