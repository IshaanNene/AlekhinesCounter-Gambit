// Board presentation helpers. Deliberately *not* a rules engine: legality is
// the game-service's job (pkg/chess), and duplicating move generation in the
// client would be a second source of truth to keep in sync. The client sends
// the move and renders whatever the server says.
//
// Square indexing mirrors the Go package exactly: a1=0, h1=7, a8=56, h8=63
// (index = rank * 8 + file).

export const START_FEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1";

/** Parse a FEN into { squares[64], turn, castling, ep, half, full }. */
export function parseFEN(fen) {
  const [placement, turn, castling, ep, half, full] = fen.trim().split(/\s+/);
  const squares = new Array(64).fill(null);

  placement.split("/").forEach((rankStr, i) => {
    const rank = 7 - i; // FEN lists rank 8 first
    let file = 0;
    for (const ch of rankStr) {
      if (ch >= "1" && ch <= "8") {
        file += Number(ch);
        continue;
      }
      squares[rank * 8 + file] = {
        type: ch.toLowerCase(),
        color: ch === ch.toUpperCase() ? "w" : "b",
      };
      file += 1;
    }
  });

  return { squares, turn, castling, ep, half: Number(half), full: Number(full) };
}

export const fileOf = (sq) => sq % 8;
export const rankOf = (sq) => Math.floor(sq / 8);
export const squareName = (sq) => "abcdefgh"[fileOf(sq)] + (rankOf(sq) + 1);

/** Parse "e2e4" / "e7e8q" into { from, to, promotion }. */
export function parseUCI(uci) {
  const sq = (s) => "abcdefgh".indexOf(s[0]) + (Number(s[1]) - 1) * 8;
  return {
    from: sq(uci.slice(0, 2)),
    to: sq(uci.slice(2, 4)),
    promotion: uci[4] ?? null,
  };
}

// One filled glyph set for both colours; CSS supplies the fill and shadow, so
// white and black pieces keep identical weight and silhouette.
const GLYPHS = { k: "♚", q: "♛", r: "♜", b: "♝", n: "♞", p: "♟" };
export const glyphFor = (piece) => GLYPHS[piece.type];

/**
 * Board square order for rendering. White's view puts rank 8 first; flipping
 * reverses it so the player's own pieces are always nearest.
 */
export function boardOrder(flipped) {
  const order = [];
  for (let rank = 7; rank >= 0; rank--) {
    for (let file = 0; file < 8; file++) order.push(rank * 8 + file);
  }
  return flipped ? order.reverse() : order;
}

/** Light squares are those where file+rank is odd (a1 is dark). */
export const isLightSquare = (sq) => (fileOf(sq) + rankOf(sq)) % 2 === 1;

/**
 * Does this move need a promotion suffix? True when a pawn reaches the far rank.
 * We auto-queen: underpromotion is rare enough that a picker would cost more UI
 * than it earns here, and the server accepts any suffix if we add one later.
 */
export function needsPromotion(squares, from, to) {
  const piece = squares[from];
  if (!piece || piece.type !== "p") return false;
  const targetRank = rankOf(to);
  return (piece.color === "w" && targetRank === 7) || (piece.color === "b" && targetRank === 0);
}

/** Format milliseconds as m:ss, or h:mm:ss past an hour. Never shows negative. */
export function formatClock(ms) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}
