// User preferences: schema, persistence, and change notification.
//
// Every preference is declared once here — label, type, default, and help text —
// and the settings panel is rendered from that declaration. Adding a preference
// means adding one entry, not touching three files.
//
// Storage is localStorage for now; once accounts land these can move server-side
// per user without changing any call site, because everything reads through
// get()/set().

const STORAGE_KEY = "acg.settings.v1";

/**
 * Preference groups. Each field:
 *   key, label, type ("toggle" | "select" | "range"), default, hint?
 *   options? (select), min/max/step/unit? (range)
 *   pending? — declared but not yet implemented; shown disabled and labelled,
 *              so the UI never promises something the backend cannot do.
 */
export const SETTINGS_SCHEMA = [
  {
    group: "Board",
    fields: [
      {
        key: "pieceSize", label: "Piece size", type: "range",
        default: 100, min: 70, max: 140, step: 5, unit: "%",
        hint: "Scale the pieces without resizing the board.",
      },
      {
        key: "coordinates", label: "Board coordinates", type: "select",
        default: "inside",
        options: [
          { value: "off", label: "Off" },
          { value: "inside", label: "Inside" },
          { value: "outside", label: "Outside" },
        ],
        hint: "Show a–h / 1–8 on the board's perimeter squares, or in the margin around it.",
      },
      {
        key: "highlightLastMove", label: "Highlight last move", type: "toggle",
        default: true,
        hint: "Mark the square a piece left and the square it landed on.",
      },
      {
        key: "showLegalMoves", label: "Show legal moves", type: "toggle",
        default: true,
        hint: "Dot every square the selected piece may move to.",
      },
      {
        key: "whiteOnBottom", label: "White always on bottom", type: "toggle",
        default: false,
        hint: "Never auto-flip the board when you play Black.",
      },
      {
        key: "focusMode", label: "Always use focus mode", type: "toggle",
        default: false,
        hint: "Hide the side panels and centre the board.",
      },
    ],
  },
  {
    group: "Piece movement",
    fields: [
      {
        key: "moveMethod", label: "Move method", type: "select",
        default: "both",
        options: [
          { value: "both", label: "Drag or click" },
          { value: "click", label: "Click squares" },
          { value: "drag", label: "Drag pieces" },
        ],
      },
      {
        key: "autoQueen", label: "Always promote to queen", type: "toggle",
        default: true,
        hint: "Hold ALT while promoting to choose a different piece instead.",
      },
      {
        key: "premoves", label: "Enable premoves", type: "toggle",
        default: false,
        hint: "Make a move during your opponent's turn; it plays automatically on yours.",
      },
      {
        key: "confirmMove", label: "Confirm move", type: "toggle",
        default: false,
        hint: "Ask before a move is played, after you make it.",
      },
    ],
  },
  {
    group: "Game",
    fields: [
      {
        key: "confirmResign", label: "Confirm resign", type: "toggle",
        default: true,
        hint: "Ask for confirmation before resigning.",
      },
      {
        key: "lowTimeWarning", label: "Low-time warning", type: "toggle",
        default: true,
        hint: "Visual and audible warning when your clock runs low.",
      },
      {
        key: "lowTimeSeconds", label: "Warn under", type: "range",
        default: 20, min: 5, max: 60, step: 5, unit: "s",
      },
    ],
  },
  {
    group: "Post-game analysis",
    // These depend on the async analysis pipeline (Kafka + engine workers) and
    // the ratings tables, which are Q3. Declared here so the roadmap is visible,
    // disabled so the UI never claims a capability that does not exist yet.
    fields: [
      {
        key: "engineEval", label: "Engine evaluation", type: "toggle",
        default: false, pending: "Q3",
        hint: "Show the engine's evaluation for each move once a game ends.",
      },
      {
        key: "moveClassification", label: "Move classification icons", type: "toggle",
        default: false, pending: "Q3",
        hint: "Brilliant, mistake, blunder markers in the move list.",
      },
      {
        key: "moveTimestamps", label: "Show timestamps", type: "toggle",
        default: false, pending: "Q3",
        hint: "How long each move took.",
      },
      {
        key: "coachRecap", label: "Post-game feedback", type: "toggle",
        default: false, pending: "Q3",
        hint: "A recap of the game's turning points.",
      },
    ],
  },
];

/** Flat map of key → field definition. */
export const FIELDS = new Map(
  SETTINGS_SCHEMA.flatMap((g) => g.fields.map((f) => [f.key, f])),
);

function defaults() {
  return Object.fromEntries([...FIELDS].map(([key, f]) => [key, f.default]));
}

let values = load();
const listeners = new Set();

function load() {
  const base = defaults();
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return base;
    const saved = JSON.parse(raw);
    // Merge over defaults and drop unknown keys, so a stored blob from an older
    // version can never resurrect a removed setting or miss a new one.
    for (const key of Object.keys(base)) {
      if (key in saved) base[key] = saved[key];
    }
  } catch {
    // Corrupt or unavailable storage is not worth failing the app over.
  }
  return base;
}

function persist() {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(values));
  } catch {
    // Private mode / quota: preferences simply do not survive the session.
  }
}

/** Current value of a preference. */
export const get = (key) => values[key];

/** All preferences, as a snapshot. */
export const all = () => ({ ...values });

/** Update a preference and notify listeners. */
export function set(key, value) {
  if (values[key] === value) return;
  values[key] = value;
  persist();
  for (const fn of listeners) fn(key, value);
}

/** Restore every preference to its default. */
export function reset() {
  values = defaults();
  persist();
  for (const fn of listeners) fn(null, null);
}

/** Subscribe to changes. Returns an unsubscribe function. */
export function onChange(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
