// Opening explorer: for the current position, what moves have been played across
// all finished games and how they scored. A read-only aggregate — the payoff of
// every archived game feeding back into a shared database of practice.

import { gql } from "./graphql.js";

const EXPLORER = `query($fen: String) {
  openingExplorer(fen: $fen, limit: 12) {
    totalGames
    moves { san uci whiteWins blackWins draws total }
  }
}`;

const $ = (id) => document.getElementById(id);

// Guard against a slow response for an old position overwriting a newer one:
// only the latest request is allowed to render.
let seq = 0;

/** Render the explorer for a FEN. A null FEN clears it. */
export async function renderExplorer(fen) {
  const el = $("explorer");
  if (!el) return;
  const mine = ++seq;

  if (!fen) {
    el.replaceChildren(empty("Start or open a game to explore its opening."));
    return;
  }

  let data;
  try {
    data = await gql(EXPLORER, { fen });
  } catch {
    if (mine === seq) el.replaceChildren(empty("Could not load the explorer."));
    return;
  }
  if (mine !== seq) return; // a newer position superseded this request

  const { moves, totalGames } = data.openingExplorer;
  el.replaceChildren();
  if (!moves.length) {
    el.append(empty("No games have reached this position yet."));
    return;
  }
  for (const m of moves) el.append(row(m, totalGames));
}

function row(m, totalGames) {
  const el = document.createElement("div");
  el.className = "explorer__row";
  el.title = `${m.san}: ${m.total} game${m.total === 1 ? "" : "s"} · ` +
    `White ${m.whiteWins}, draw ${m.draws}, Black ${m.blackWins}`;

  const san = document.createElement("span");
  san.className = "explorer__san";
  san.textContent = m.san;

  const count = document.createElement("span");
  count.className = "explorer__count";
  const share = totalGames ? Math.round((m.total / totalGames) * 100) : 0;
  count.textContent = `${m.total} · ${share}%`;

  el.append(san, count, resultBar(m));
  return el;
}

// A proportional White / draw / Black result bar.
function resultBar(m) {
  const bar = document.createElement("div");
  bar.className = "explorer__bar";
  const total = m.total || 1;
  for (const [cls, n] of [["w", m.whiteWins], ["d", m.draws], ["b", m.blackWins]]) {
    const seg = document.createElement("span");
    seg.className = cls;
    seg.style.width = `${(n / total) * 100}%`;
    bar.append(seg);
  }
  return bar;
}

function empty(text) {
  const p = document.createElement("p");
  p.className = "explorer__empty";
  p.textContent = text;
  return p;
}
