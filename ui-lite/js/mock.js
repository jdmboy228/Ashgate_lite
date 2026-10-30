// ?mock=<frame>&theme=<light|dark> renders exactly one design state from
// mock/mock-states.json (design docs). Loaded only in mock mode.

const mockIcons = {
  "Chrome": "app-chrome.svg",
  "Telegram": "app-telegram.svg",
  "Dolphin Anty": "app-dolphin-anty.svg",
  "Dolphin Anty Beta": "app-dolphin-28.svg",
  "Discord": "app-discord.svg",
};

// "-60s" means sixty seconds before now.
function when(value) {
  const m = typeof value === "string" && value.match(/^(-?\d+)s$/);
  return m ? Date.now() + Number(m[1]) * 1000 : value;
}

function withIcon(item, size) {
  const file = size === 28 && item.name === "Dolphin Anty" ? "app-dolphin-28.svg" : mockIcons[item.name];
  return file ? { ...item, iconDataUrl: new URL(`../mock/icons/${file}`, import.meta.url).href } : item;
}

export async function loadMock(frame, theme) {
  const res = await fetch(new URL("../mock/mock-states.json", import.meta.url));
  const all = await res.json();
  const entry = all[frame];
  if (!entry) throw new Error(`unknown mock frame ${frame}`);
  const state = structuredClone(entry.state);
  if (theme === "light" || theme === "dark") state.theme = theme;
  state.apps = state.apps.map((a) => withIcon(a, 24));
  for (const p of state.proxies) p.unavailableSince = when(p.unavailableSince);
  if (state.proxyDown) state.proxyDown.sinceMs = when(state.proxyDown.sinceMs);
  const ui = structuredClone(entry.ui || {});
  ui.suggestions = (ui.suggestions || []).map((s) => withIcon(s, 24));
  ui.results = (ui.results || []).map((r) => withIcon(r, 28));
  return { state, ui };
}
