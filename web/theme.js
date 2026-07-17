// Light/dark theme.
//
// A single `data-theme` attribute on <html> is the source of truth; styles.css
// keys the dark palette off it. The initial value is set by an inline snippet
// in index.html <head>, before first paint, so the page never flashes the wrong
// palette while this module loads. Here we own the toggle, persistence, and
// keeping the button in sync — nothing reads the theme except through here.

const STORAGE_KEY = "acg.theme";

/** The active theme, read from the attribute the head snippet already set. */
export function current() {
  return document.documentElement.dataset.theme === "dark" ? "dark" : "light";
}

function apply(theme) {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // localStorage can throw in private mode; the in-page attribute still holds.
  }
}

/** Flip light↔dark and persist. Returns the new theme. */
export function toggle() {
  const next = current() === "dark" ? "light" : "dark";
  apply(next);
  return next;
}

/**
 * Wire a button to the toggle, keeping its glyph and label in sync. The button
 * shows the mode it will switch *to* (a sun while dark, a moon while light) —
 * the affordance users expect from a theme switch.
 */
export function mountToggle(button) {
  if (!button) return;
  const sync = () => {
    const to = current() === "dark" ? "light" : "dark";
    button.textContent = to === "light" ? "☀" : "☾";
    button.setAttribute("aria-label", `Switch to ${to} mode`);
    button.setAttribute("title", `Switch to ${to} mode`);
  };
  sync();
  button.addEventListener("click", () => {
    toggle();
    sync();
  });
}
