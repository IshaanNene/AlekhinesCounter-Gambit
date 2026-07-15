// Renders the settings drawer from SETTINGS_SCHEMA. Knows nothing about chess —
// it only reads the schema, writes through settings.set(), and lets the app
// react via settings.onChange().

import { SETTINGS_SCHEMA, get, set, reset } from "./settings.js";

/** Build the drawer once and wire it to the toggle button. */
export function mountSettings({ openButton, drawer }) {
  drawer.replaceChildren(header(), ...SETTINGS_SCHEMA.map(groupSection), footer());

  const close = () => {
    drawer.classList.remove("is-open");
    openButton.setAttribute("aria-expanded", "false");
  };
  const open = () => {
    drawer.classList.add("is-open");
    openButton.setAttribute("aria-expanded", "true");
    drawer.querySelector("button, input, select")?.focus();
  };

  openButton.addEventListener("click", () =>
    drawer.classList.contains("is-open") ? close() : open(),
  );
  drawer.querySelector("[data-close]").addEventListener("click", close);
  drawer.querySelector("[data-reset]").addEventListener("click", () => {
    reset();
    // Re-render so every control reflects the restored defaults.
    mountSettings({ openButton, drawer });
    open();
  });

  // Escape closes; a click outside dismisses.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && drawer.classList.contains("is-open")) close();
  });
  document.addEventListener("pointerdown", (e) => {
    if (!drawer.classList.contains("is-open")) return;
    if (drawer.contains(e.target) || openButton.contains(e.target)) return;
    close();
  });
}

function header() {
  const el = document.createElement("div");
  el.className = "drawer__head";
  el.innerHTML = `<h2 class="drawer__title">Settings</h2>`;
  const btn = document.createElement("button");
  btn.className = "btn btn--icon btn--ghost";
  btn.dataset.close = "";
  btn.setAttribute("aria-label", "Close settings");
  btn.textContent = "✕";
  el.append(btn);
  return el;
}

function footer() {
  const el = document.createElement("div");
  el.className = "drawer__foot";
  const btn = document.createElement("button");
  btn.className = "btn btn--ghost";
  btn.dataset.reset = "";
  btn.textContent = "Restore defaults";
  el.append(btn);
  return el;
}

function groupSection(group) {
  const section = document.createElement("section");
  section.className = "drawer__group";

  const title = document.createElement("h3");
  title.className = "block__title";
  title.textContent = group.group;
  section.append(title);

  for (const field of group.fields) section.append(row(field));
  return section;
}

function row(field) {
  const el = document.createElement("div");
  el.className = "setting" + (field.pending ? " is-pending" : "");

  const text = document.createElement("div");
  text.className = "setting__text";

  const label = document.createElement("label");
  label.className = "setting__label";
  label.setAttribute("for", `set-${field.key}`);
  label.textContent = field.label;
  if (field.pending) {
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = field.pending;
    badge.title = `Not implemented yet — planned for ${field.pending}.`;
    label.append(badge);
  }
  text.append(label);

  if (field.hint) {
    const hint = document.createElement("p");
    hint.className = "setting__hint";
    hint.textContent = field.hint;
    text.append(hint);
  }

  el.append(text, control(field));
  return el;
}

function control(field) {
  switch (field.type) {
    case "toggle": return toggle(field);
    case "select": return select(field);
    case "range": return range(field);
    default: return document.createElement("span");
  }
}

/** A neumorphic switch: an inset track with a convex knob that slides in it. */
function toggle(field) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.id = `set-${field.key}`;
  btn.className = "switch";
  btn.setAttribute("role", "switch");
  btn.disabled = Boolean(field.pending);

  const knob = document.createElement("span");
  knob.className = "switch__knob";
  btn.append(knob);

  const paint = () => {
    const on = Boolean(get(field.key));
    btn.classList.toggle("is-on", on);
    btn.setAttribute("aria-checked", String(on));
  };
  paint();

  btn.addEventListener("click", () => {
    set(field.key, !get(field.key));
    paint();
  });
  return btn;
}

function select(field) {
  const el = document.createElement("select");
  el.id = `set-${field.key}`;
  el.className = "select";
  el.disabled = Boolean(field.pending);
  for (const opt of field.options) {
    const o = document.createElement("option");
    o.value = opt.value;
    o.textContent = opt.label;
    o.selected = get(field.key) === opt.value;
    el.append(o);
  }
  el.addEventListener("change", () => set(field.key, el.value));
  return el;
}

function range(field) {
  const wrap = document.createElement("div");
  wrap.className = "setting__range";

  const out = document.createElement("output");
  out.className = "field__value";
  out.textContent = `${get(field.key)}${field.unit ?? ""}`;

  const input = document.createElement("input");
  input.type = "range";
  input.id = `set-${field.key}`;
  input.className = "slider";
  input.min = field.min;
  input.max = field.max;
  input.step = field.step ?? 1;
  input.value = get(field.key);
  input.disabled = Boolean(field.pending);

  input.addEventListener("input", () => {
    const v = Number(input.value);
    out.textContent = `${v}${field.unit ?? ""}`;
    set(field.key, v);
  });

  wrap.append(out, input);
  return wrap;
}
