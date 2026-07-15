// Account UI: three ways in, plus profile and history.
//
// The session is an httpOnly cookie set by the gateway, so this module never
// touches a token — it just calls mutations and re-reads `me`. That is the point:
// a token the page cannot read is a token XSS cannot steal.

import { gql } from "./graphql.js";

const USER_FIELDS = `id username email elo isGuest gamesPlayed`;

const ME = `query { me { ${USER_FIELDS} } }`;
const LOGIN_AS_GUEST = `mutation { loginAsGuest { user { ${USER_FIELDS} } } }`;
const LOGIN = `mutation($in: LoginInput!) { login(input: $in) { user { ${USER_FIELDS} } } }`;
const REGISTER = `mutation($in: RegisterInput!) { register(input: $in) { user { ${USER_FIELDS} } } }`;
const REQUEST_TOKEN = `mutation($e: String!) { requestLoginToken(email: $e) { token deliveredInBand } }`;
const REDEEM_TOKEN = `mutation($t: String!) { redeemLoginToken(token: $t) { user { ${USER_FIELDS} } } }`;
const LOGOUT = `mutation { logout }`;
const HISTORY = `query($limit: Int) {
  gameHistory(limit: $limit) {
    total
    games { id whiteName blackName vsEngine rated status eloDelta whiteId moveCount startedAt }
  }
}`;

const $ = (id) => document.getElementById(id);

/** Current user, or null when signed out. */
export let me = null;

const listeners = new Set();
/** Subscribe to sign-in / sign-out. */
export const onAccountChange = (fn) => { listeners.add(fn); return () => listeners.delete(fn); };
const emit = () => { for (const fn of listeners) fn(me); };

/** Re-read the session from the server. */
export async function refreshMe() {
  try {
    const data = await gql(ME);
    me = data.me;
  } catch {
    me = null;
  }
  paint();
  emit();
  return me;
}

/** Sign in as a guest — used by the "play now" path when signed out. */
export async function ensureSignedIn() {
  if (me) return me;
  const data = await gql(LOGIN_AS_GUEST);
  me = data.loginAsGuest.user;
  paint();
  emit();
  return me;
}

/* ── Rendering ──────────────────────────────────────────────────────────── */

function paint() {
  $("account-name").textContent = me ? me.username : "Sign in";
  $("account-elo").textContent = me ? me.elo : "—";
  $("account-btn").title = me
    ? `${me.username} — ${me.elo}${me.isGuest ? " (guest)" : ""}`
    : "Sign in";

  $("signed-out").hidden = Boolean(me);
  $("signed-in").hidden = !me;
  $("account-title").textContent = me ? "Account" : "Sign in";

  if (me) {
    $("prof-name").textContent = me.username + (me.isGuest ? " (guest)" : "");
    $("prof-elo").textContent = me.elo;
    $("prof-games").textContent = me.gamesPlayed;
    // Only a guest has anything to claim.
    $("upgrade-block").hidden = !me.isGuest;
  }
}

function resultFor(g) {
  if (g.status === "IN_PROGRESS") return { label: "LIVE", cls: "" };
  if (g.status === "DRAW") return { label: "DRAW", cls: "is-draw" };
  const iWasWhite = me && g.whiteId === me.id;
  const whiteWon = g.status === "WHITE_WON";
  const won = iWasWhite === whiteWon;
  return won ? { label: "WIN", cls: "is-win" } : { label: "LOSS", cls: "is-loss" };
}

async function renderHistory(onOpen) {
  const list = $("history");
  list.replaceChildren();
  if (!me) return;

  let data;
  try {
    data = await gql(HISTORY, { limit: 15 });
  } catch {
    list.append(row("history__empty", "Could not load history."));
    return;
  }
  const games = data.gameHistory.games;
  if (!games.length) {
    list.append(row("history__empty", "No games yet — play one!"));
    return;
  }

  for (const g of games) {
    const li = document.createElement("li");
    li.className = "history__row";

    const iWasWhite = g.whiteId === me.id;
    const opponent = iWasWhite ? g.blackName : g.whiteName;

    const who = document.createElement("div");
    who.className = "history__opp";
    who.innerHTML = `${opponent || "Open seat"} <span>· ${iWasWhite ? "white" : "black"} · ${g.moveCount} moves${g.rated ? " · rated" : ""}</span>`;

    const res = resultFor(g);
    const badge = document.createElement("span");
    badge.className = `history__result ${res.cls}`;
    badge.textContent = res.label;

    const delta = document.createElement("span");
    delta.className = "history__delta";
    if (g.eloDelta !== null && g.eloDelta !== undefined) {
      delta.textContent = g.eloDelta > 0 ? `+${g.eloDelta}` : String(g.eloDelta);
      delta.classList.add(g.eloDelta > 0 ? "is-up" : g.eloDelta < 0 ? "is-down" : "");
    } else {
      delta.textContent = "—";
    }

    li.append(who, badge, delta);
    li.addEventListener("click", () => onOpen(g.id));
    list.append(li);
  }
}

function row(cls, text) {
  const li = document.createElement("li");
  li.className = cls;
  li.textContent = text;
  return li;
}

/* ── Wiring ─────────────────────────────────────────────────────────────── */

/**
 * @param {(msg: string, isError?: boolean) => void} toast
 * @param {(err: unknown) => string} friendlyError
 * @param {(gameId: string) => void} onOpenGame
 */
export function mountAccount({ toast, friendlyError, onOpenGame }) {
  const scrim = $("account-scrim");
  const open = () => { scrim.hidden = false; paint(); renderHistory(onOpenGame); };
  const close = () => { scrim.hidden = true; };

  $("account-btn").addEventListener("click", open);
  $("account-close").addEventListener("click", close);
  scrim.addEventListener("click", (e) => { if (e.target === scrim) close(); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !scrim.hidden) close();
  });

  for (const btn of document.querySelectorAll(".tabs__btn")) {
    btn.addEventListener("click", () => {
      for (const b of document.querySelectorAll(".tabs__btn")) {
        b.classList.toggle("is-active", b === btn);
      }
      $("tab-password").hidden = btn.dataset.tab !== "password";
      $("tab-magic").hidden = btn.dataset.tab !== "magic";
    });
  }

  const run = async (fn) => {
    try {
      await fn();
      await refreshMe();
      renderHistory(onOpenGame);
    } catch (err) {
      toast(friendlyError(err), true);
    }
  };

  $("do-guest").addEventListener("click", () => run(async () => {
    await gql(LOGIN_AS_GUEST);
    toast("Playing as a guest.");
    close();
  }));

  $("do-login").addEventListener("click", () => run(async () => {
    await gql(LOGIN, {
      in: { identifier: $("auth-identifier").value.trim(), password: $("auth-password").value },
    });
    toast("Signed in.");
    close();
  }));

  $("do-register").addEventListener("click", () => run(async () => {
    await gql(REGISTER, {
      in: {
        username: $("auth-identifier").value.trim(),
        password: $("auth-password").value,
        email: $("auth-email").value.trim() || null,
        // Upgrade in place when already a guest, so the rating survives signup.
        upgradeCurrentGuest: Boolean(me?.isGuest),
      },
    });
    toast("Account created.");
    close();
  }));

  $("do-upgrade").addEventListener("click", () => {
    // Reuse the sign-up form; the guest flag makes register() upgrade in place.
    $("signed-out").hidden = false;
    $("signed-in").hidden = true;
    $("auth-identifier").focus();
    toast("Pick a username and password to claim this account.");
  });

  $("do-magic").addEventListener("click", () => run(async () => {
    const data = await gql(REQUEST_TOKEN, { e: $("magic-email").value.trim() });
    const { token, deliveredInBand } = data.requestLoginToken;
    if (deliveredInBand && token) {
      // Dev convenience: no mail provider, so the server hands the token back.
      $("magic-token").value = token;
      $("magic-result").hidden = false;
    } else {
      toast("If that address has an account, a sign-in link is on its way.");
    }
  }));

  $("do-redeem").addEventListener("click", () => run(async () => {
    await gql(REDEEM_TOKEN, { t: $("magic-token").value.trim() });
    toast("Signed in.");
    close();
  }));

  $("do-logout").addEventListener("click", () => run(async () => {
    await gql(LOGOUT);
    toast("Signed out.");
    close();
  }));
}
