// Small building blocks shared by every screen.
import { h, icon, text } from "../dom.js";
import { ping as pingText } from "../format.js";

// TitleBar 32 px: logo or settings icon, title, theme toggle, minimize, close.
export function titleBar(ctx, { secondary = false } = {}) {
  const dark = ctx.state.theme === "dark";
  const start = (e) => {
    if (e.button === 0 && !e.target.closest("button")) ctx.call("window.startDrag");
  };
  return h("header", { class: "titlebar", onmousedown: start },
    secondary ? icon("settings-16", "c-text2") : icon("ashgate-logo"),
    text("title", secondary ? "Управление прокси — Ashgate Lite" : "Ashgate Lite"),
    h("div", { class: "spacer" }),
    h("div", { class: "caption" },
      h("button", {
        class: "theme-toggle", "data-key": "theme",
        title: dark ? "Светлая тема" : "Тёмная тема", "aria-label": dark ? "Светлая тема" : "Тёмная тема",
        onclick: () => ctx.setTheme(dark ? "light" : "dark"),
      }, h("span", { class: "only-light" }, icon("weather-moon-16")), h("span", { class: "only-dark" }, icon("weather-sunny-16"))),
      h("div", { class: "gap" }),
      !secondary && h("button", { class: "caption-btn", "aria-label": "Свернуть", onclick: () => ctx.call("window.minimize") }, icon("subtract-16")),
      h("button", { class: "caption-btn", "aria-label": "Закрыть", onclick: () => ctx.call("window.close") }, icon("dismiss-16")),
    ),
  );
}

export const typeChip = (type) => h("span", { class: "chip chip-type" }, type);

export const currentChip = () => h("span", { class: "chip chip-current" }, "текущий");

export const proxyDot = (slot, size = 10) => h("span", { class: `dot dot-${size} dot-p${slot}` });

export const directDot = () => h("span", { class: "dot dot-8 dot-direct" });

// Ping: three bars and "48 мс". The design has no rule for fewer bars.
// TODO: insufficient data: thresholds for one or two ping bars.
export function pingView(ms) {
  if (ms === null || ms === undefined) return null;
  return h("span", { class: "ping" }, h("span", { class: "bars" }, h("i"), h("i"), h("i")), pingText(ms));
}

export function errorText(lastError) {
  // TODO: insufficient data: the design only names a timeout («таймаут»).
  return lastError === "timeout" ? "таймаут" : "ошибка";
}

const statuses = {
  running: () => h("span", { class: "status status-ok" }, icon("checkmark-circle-12"), "Работает"),
  notRunning: () => h("span", { class: "status status-idle" }, h("span", { class: "ring" }), "Не запущено"),
  error: () => h("span", { class: "status status-err" }, icon("error-circle-12"), "Ошибка"),
  checking: () => h("span", { class: "status status-chk" }, icon("arrow-sync-12"), "Проверяем…"),
};

export const statusPill = (status) => statuses[status]?.() || null;

// The real .exe icon from the backend, or the neutral app/exe square.
export function appIcon(app, size = 24) {
  if (app.iconDataUrl) {
    return h("img", { class: `app-icon${size === 28 ? " app-icon-28" : ""}`, src: app.iconDataUrl, alt: "" });
  }
  // TODO: insufficient data: the neutral square is only drawn at 28 px; 24 px uses the same shape scaled.
  return h("span", { class: `exe-icon${size === 24 ? " small" : ""}` }, icon("window-20"));
}

export function retryButton(ctx, { kind = "primary", checking = false, tight = false, onclick }) {
  return h("button", {
    class: `retry retry-${kind}${tight ? " tight" : ""}`,
    disabled: checking,
    onclick,
  }, icon("arrow-sync-16", kind === "primary" ? "c-onAccent" : "c-text"), checking ? "Проверяем…" : "Проверить снова");
}
