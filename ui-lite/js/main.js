// Ashgate Lite UI entry point. One page serves both windows:
// the main window and, with ?window=manage, «Управление прокси».
import { liveBridge, mockBridge } from "./bridge.js";
import { speed } from "./format.js";
import { renderAddApp } from "./view/add-app.js";
import { renderMain } from "./view/main-screen.js";
import { renderManage } from "./view/manage.js";

const params = new URLSearchParams(location.search);
const root = document.getElementById("app");

const store = {
  state: null,
  ui: {},
  bridge: null,
  manage: params.get("window") === "manage",
};

let frame = 0;
function scheduleRender() {
  if (!frame) frame = requestAnimationFrame(render);
}

function render() {
  frame = 0;
  if (!store.state) return;
  document.documentElement.dataset.theme = store.state.theme;
  const focusKey = document.activeElement?.dataset?.key;
  const caret = document.activeElement?.selectionStart;
  const ctx = context();
  const view = store.manage || store.ui.window === "manage"
    ? renderManage(ctx)
    : store.ui.screen === "addApp" ? renderAddApp(ctx) : renderMain(ctx);
  root.replaceChildren(view);
  restoreFocus(focusKey, caret);
}

// Re-rendering replaces the DOM, so keyboard focus moves back by data-key.
function restoreFocus(key, caret) {
  const el = (key && root.querySelector(`[data-key="${CSS.escape(key)}"]`)) || root.querySelector("[autofocus]");
  if (!el) return;
  el.focus({ preventScroll: true });
  if (caret !== undefined && caret !== null && el.setSelectionRange) el.setSelectionRange(caret, caret);
}

const call = (cmd, args) => store.bridge.call(cmd, args).catch((err) => {
  // TODO: insufficient data: the design has no error message for failed commands (PROXY_PARSE, PROXY_LIMIT).
  console.warn(cmd, err.code || "", err.message);
  throw err;
});
const quietCall = (cmd, args) => call(cmd, args).catch(() => null);

function setUI(patch, rerender = true) {
  Object.assign(store.ui, patch);
  if (rerender) scheduleRender();
}

function context() {
  return {
    state: store.state,
    ui: store.ui,
    now: store.ui.now || Date.now(),
    call: quietCall,
    setUI,
    setTheme(theme) {
      store.state = { ...store.state, theme };
      scheduleRender();
      quietCall("settings.setTheme", { theme });
    },
    togglePicker() {
      const open = !store.ui.pickerOpen;
      setUI({ pickerOpen: open, footerOpen: false, rowMenu: null });
      if (open) quietCall("proxy.ping", {});
    },
    toggleFooterMenu() {
      setUI({ footerOpen: !store.ui.footerOpen, pickerOpen: false, rowMenu: null });
    },
    openRowMenu(appId, button) {
      setUI({ rowMenu: { appId, style: rowMenuStyle(button) }, pickerOpen: false, footerOpen: false });
    },
    closeMenus,
    async checkProxy(proxyId) {
      setUI({ checkingProxy: proxyId });
      await quietCall("proxy.check", { proxyId });
      setUI({ checkingProxy: null });
    },
    openAddApp() {
      setUI({ screen: "addApp", query: "", results: [], selectedIndex: 0, pickerOpen: false, footerOpen: false, rowMenu: null });
      searchApps("");
    },
    closeAddApp,
    searchApps,
    selectResult: (i) => setUI({ selectedIndex: i }),
    addSelected,
    addApp: (exePath) => quietCall("apps.add", { exePath }),
    pasteProxy,
    async editProxy(proxyId) {
      const link = await quietCall("proxy.link", { proxyId });
      setUI({ editProxyId: proxyId, editLink: link || "" });
    },
    async saveProxy() {
      await call("proxy.update", { proxyId: store.ui.editProxyId, text: store.ui.editLink || "" })
        .then(closeEdit, () => null);
    },
    closeEdit,
    async removeEdited(proxyId) {
      await quietCall("proxy.remove", { proxyId });
      closeEdit();
    },
  };
}

function closeMenus() {
  setUI({ pickerOpen: false, footerOpen: false, rowMenu: null });
}

function closeAddApp() {
  setUI({ screen: null, query: "", results: [] });
}

function closeEdit() {
  setUI({ editProxyId: null, editLink: "" });
}

// The ⋯ menu opens under its button, or above it near the bottom edge.
function rowMenuStyle(button) {
  const win = root.firstElementChild.getBoundingClientRect();
  const b = button.getBoundingClientRect();
  const right = Math.round(win.right - b.right);
  const below = b.bottom - win.top + 4;
  const menuHeight = 146;
  return below + menuHeight < win.height
    ? `right:${right}px;top:${Math.round(below)}px`
    : `right:${right}px;bottom:${Math.round(win.bottom - b.top + 4)}px`;
}

let searchTimer = 0;
function searchApps(query) {
  setUI({ query }, false);
  clearTimeout(searchTimer);
  searchTimer = setTimeout(async () => {
    const results = await quietCall("apps.suggest", { query });
    if (store.ui.query === query) setUI({ results: results || [], selectedIndex: 0 });
  }, 120);
}

async function addSelected() {
  const r = (store.ui.results || [])[store.ui.selectedIndex || 0];
  if (!r) return;
  await quietCall("apps.add", { exePath: r.exePath });
  closeAddApp();
}

function pasteProxy(text) {
  if (text && text.trim()) quietCall("proxy.paste", { text: text.trim() });
}

// ---------- backend events ----------

function onEvent(msg) {
  switch (msg.event) {
    case "state":
      store.state = msg.data;
      scheduleRender();
      break;
    case "speeds":
      applySpeeds(msg.data);
      break;
    case "ping":
      patchProxy(msg.data);
      break;
    case "appStatus":
      patchApp(msg.data);
      break;
  }
}

// Speeds change once a second; only their text nodes are updated.
function applySpeeds({ apps = {}, allTraffic }) {
  if (!store.state) return;
  for (const app of store.state.apps) Object.assign(app, apps[app.id] || {});
  if (allTraffic) store.state.allTraffic = allTraffic;
  for (const el of root.querySelectorAll("[data-speed]")) {
    const id = el.dataset.speed;
    const s = id === "*" ? store.state.allTraffic : apps[id];
    if (s) el.textContent = speed(s.downBps, s.upBps);
  }
}

function patchProxy({ proxyId, pingMs, available }) {
  const p = store.state?.proxies.find((x) => x.id === proxyId);
  if (!p) return;
  Object.assign(p, { pingMs, available });
  scheduleRender();
}

function patchApp({ appId, status, error }) {
  const app = store.state?.apps.find((a) => a.id === appId);
  if (!app) return;
  Object.assign(app, { status, error });
  scheduleRender();
}

// ---------- keyboard and clipboard ----------

function onKey(e) {
  if (e.ctrlKey && !e.shiftKey && !e.altKey && e.code === "KeyP") {
    e.preventDefault();
    quietCall("window.openManage");
    return;
  }
  if (e.key === "Escape") return onEscape(e);
  if (store.ui.screen === "addApp") onAddAppKey(e);
}

function onEscape(e) {
  if (store.ui.pickerOpen || store.ui.footerOpen || store.ui.rowMenu) closeMenus();
  else if (store.ui.screen === "addApp") closeAddApp();
  else if (store.ui.editProxyId) closeEdit();
  else return;
  e.preventDefault();
}

function onAddAppKey(e) {
  const n = (store.ui.results || []).length;
  if (!n) return;
  const i = store.ui.selectedIndex || 0;
  if (e.key === "ArrowDown") setUI({ selectedIndex: (i + 1) % n });
  else if (e.key === "ArrowUp") setUI({ selectedIndex: (i - 1 + n) % n });
  else if (e.key === "Enter") addSelected();
  else return;
  e.preventDefault();
}

// Ctrl+V anywhere outside a text field pastes a proxy link.
function onPaste(e) {
  if (e.target.closest?.("input")) return;
  e.preventDefault();
  pasteProxy(e.clipboardData?.getData("text"));
}

function onPointerDown(e) {
  if (e.target.closest("[data-menu], [aria-haspopup]")) return;
  if (store.ui.pickerOpen || store.ui.footerOpen || store.ui.rowMenu) closeMenus();
}

// ---------- boot ----------

async function boot() {
  document.addEventListener("keydown", onKey);
  document.addEventListener("paste", onPaste);
  document.addEventListener("pointerdown", onPointerDown);
  const mock = params.get("mock");
  if (mock) {
    const { loadMock } = await import("./mock.js");
    const { state, ui } = await loadMock(mock, params.get("theme"));
    store.ui = ui;
    store.bridge = mockBridge(onEvent, state);
    onEvent({ event: "state", data: state });
    return;
  }
  store.bridge = liveBridge(onEvent);
  onEvent({ event: "state", data: await call("state.get") });
  if (!store.manage) store.ui.suggestions = (await quietCall("apps.suggest", { query: "" })) || [];
  scheduleRender();
}

boot();
