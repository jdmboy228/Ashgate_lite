// Main window: screens a, b, c, d, g, h, i, j, l (design docs/screens.md).
import { h, icon, text } from "../dom.js";
import { minutesSince, plural, proxyName, secondsSince, speed } from "../format.js";
import {
  appIcon, directDot, errorText, pingView, proxyDot, retryButton, statusPill, titleBar, typeChip,
} from "./common.js";

export function renderMain(ctx) {
  const view = mainView(ctx.state);
  return h("div", { class: "win" },
    titleBar(ctx),
    hero(ctx, view),
    h("div", { class: "current-wrap" }, view.empty ? emptyPill() : currentPill(ctx, view)),
    view.down?.fastest && suggestion(ctx, view),
    view.empty ? firstRunList(ctx) : appSection(ctx, view),
    footer(ctx, view),
    ctx.ui.pickerOpen && !view.empty && proxyPicker(ctx, view),
    ctx.ui.footerOpen && footerMenu(ctx, view),
    ctx.ui.rowMenu && rowMenu(ctx, view),
  );
}

// mainView derives what the screen shows from the backend State.
function mainView(state) {
  const byId = Object.fromEntries(state.proxies.map((p) => [p.id, p]));
  const othersId = state.others === "direct" ? null : state.others.proxyId;
  const down = state.proxyDown && { ...state.proxyDown, proxy: byId[state.proxyDown.proxyId] };
  if (down?.fastest) down.fastest.proxy = byId[down.fastest.proxyId];
  return {
    empty: state.proxies.length === 0,
    current: byId[state.currentProxyId] || null,
    others: othersId ? byId[othersId] : null,
    down: down?.proxy ? down : null,
    byId,
  };
}

// ---------- hero ----------

function hero(ctx, view) {
  const { state } = ctx;
  const toggleClass = state.enabled ? (view.down ? "on down" : "on") : "off";
  const [headline, subtitle] = heroText(state, view);
  return h("section", { class: "hero" },
    h("button", {
      class: `big-toggle ${toggleClass}`, role: "switch", "aria-checked": String(state.enabled),
      "aria-label": "Пустить через прокси", "data-key": "toggle", disabled: view.empty,
      onclick: () => ctx.call("toggle.set", { enabled: !state.enabled }),
    }, h("span", { class: "knob" })),
    text(`headline${state.enabled && view.down ? " down" : ""}`, headline),
    text("subtitle", subtitle),
  );
}

function heroText(state, view) {
  if (view.empty) return ["Пока всё идёт напрямую", "Скопируйте ссылку прокси — и выберите приложения"];
  if (!state.enabled) return ["Пока всё идёт напрямую", "Выключено · включите, чтобы пустить через прокси"];
  if (view.down) return [`${proxyName(view.down.proxy.slot)} не отвечает`, "Приложения на паузе — IP не утёк"];
  if (view.others) {
    const p = view.others;
    return [`Весь трафик → ${proxyName(p.slot)}`, ["Все приложения Windows", p.type, p.country].filter(Boolean).join(" · ")];
  }
  const apps = state.apps.filter((a) => a.status !== "paused");
  const n = apps.length;
  const headline = `${n} ${plural(n, "приложение", "приложения", "приложений")} идут через прокси`;
  const checking = apps.find((a) => a.status === "checking");
  if (checking) return [headline, `Проверяем ${checking.name}…`];
  return [headline, summary(apps)];
}

// "2 работают · 1 не запущено · 1 с ошибкой"; zero parts are dropped.
function summary(apps) {
  const count = (s) => apps.filter((a) => a.status === s).length;
  const k = count("running");
  const m = count("notRunning");
  const e = count("error");
  return [
    k && `${k} ${plural(k, "работает", "работают", "работают")}`,
    m && `${m} не запущено`,
    e && `${e} с ошибкой`,
  ].filter(Boolean).join(" · ");
}

// ---------- current proxy pill ----------

function emptyPill() {
  const dash = h("span", { class: "dash" });
  dash.innerHTML = '<svg width="100%" height="100%"><rect style="x:0.5px;y:0.5px;width:calc(100% - 1px);height:59px;rx:29.5px" fill="none" stroke-width="1" stroke-dasharray="4 4"/></svg>';
  return h("div", { class: "pill pill-empty pill-width" },
    dash,
    icon("clipboard-paste-20", "c-text2"),
    h("div", { class: "text col fill" },
      text("lead", "Прокси ещё нет"),
      text("hint ellipsis", "Скопируйте ссылку — она вставится сама"),
    ),
    h("span", { class: "chip chip-key" }, "Ctrl+V"),
  );
}

function currentPill(ctx, view) {
  const p = view.current;
  if (!p) return directPill(ctx);
  const down = view.down && view.down.proxyId === p.id;
  const state = down ? " error" : ctx.ui.pickerOpen ? " open" : "";
  const open = () => ctx.togglePicker();
  return h("div", {
    class: `pill pill-width${state}`, role: "button", tabindex: "0", "data-key": "pill",
    "aria-haspopup": "menu", "aria-expanded": String(!!ctx.ui.pickerOpen),
    onclick: open, onkeydown: (e) => (e.key === "Enter" || e.key === " ") && (e.preventDefault(), open()),
  },
    h("span", { class: `badge p${p.slot}` }, String(p.slot)),
    h("div", { class: "text col fill" },
      h("div", { class: "title-row" }, text("place", placeName(p)), typeChip(p.type)),
      down
        ? text("addr down ellipsis", `Не отвечает ${secondsSince(view.down.sinceMs, ctx.now)} с`)
        : text("addr ellipsis", p.hostMasked),
    ),
    down
      // A proxy that stopped answering without a recorded cause timed out.
      ? h("span", { class: "ping-err f-monoAddr" }, icon("error-circle-16", "c-danger"), errorText(p.lastError || "timeout"))
      : pingView(p.pingMs),
    h("button", {
      class: "btn btn-subtle icon-only", "aria-label": "Копировать ссылку",
      onclick: (e) => { e.stopPropagation(); ctx.call("proxy.copy", { proxyId: p.id }); },
    }, icon("copy-16", "c-text2")),
    icon("chevron-down-16", "c-text2"),
  );
}

// TODO: insufficient data: no pill design for «Напрямую» as the current choice; it reuses the menu item.
function directPill(ctx) {
  return h("div", {
    class: `pill pill-width${ctx.ui.pickerOpen ? " open" : ""}`, role: "button", tabindex: "0", "data-key": "pill",
    onclick: () => ctx.togglePicker(),
  },
    h("span", { class: "others-icon" }, icon("globe-16")),
    h("div", { class: "text col fill" }, text("place", "Напрямую"), text("addr ellipsis", "Без прокси — сайты видят ваш IP")),
    icon("chevron-down-16", "c-text2"),
  );
}

// The pill title is the proxy's place (country); without one, its name.
const placeName = (p) => p.country || proxyName(p.slot);

// ---------- proxy picker (d) ----------

// fastestId is the quickest available proxy other than the current one
// (the «быстрый» tag). TODO: insufficient data: behavior.md §3 also says no
// proxy is tagged when the current one is the fastest, but its own example
// for screen d (current Прокси 1 at 48 мс, tag on Прокси 3 at 64 мс)
// contradicts that; the mockup's rule is used.
function fastestId(proxies, currentId) {
  const ok = proxies.filter((p) => p.id !== currentId && p.available && p.pingMs !== null && p.pingMs !== undefined);
  if (!ok.length) return null;
  return ok.reduce((a, b) => (b.pingMs < a.pingMs ? b : a)).id;
}

function proxyPicker(ctx, view) {
  const { state } = ctx;
  const proxies = [...state.proxies].sort((a, b) => a.slot - b.slot);
  const fast = fastestId(proxies, state.currentProxyId);
  const choose = (proxyId) => { ctx.call("proxy.select", { proxyId }); ctx.closeMenus(); };
  return h("div", { class: "flyout picker", role: "menu", "data-menu": "picker" },
    h("div", { class: "picker-head" },
      text("label fill", "Текущий прокси"),
      text("note", "пинг сейчас"),
      h("button", { class: "btn btn-subtle icon-only", "aria-label": "Обновить пинг", onclick: () => ctx.call("proxy.ping", {}) },
        icon("arrow-sync-16", "c-text2")),
    ),
    proxies.map((p) => proxyItem(ctx, p, {
      selected: p.id === state.currentProxyId, fast: p.id === fast, onpick: () => choose(p.id),
    })),
    h("div", { class: "divider" }),
    directItem(state.currentProxyId === null, () => choose(null)),
    h("div", { class: "divider" }),
    h("button", { class: "menu-item manage-item", role: "menuitem", onclick: () => { ctx.closeMenus(); ctx.call("window.openManage"); } },
      icon("settings-16", "c-text2"),
      text("label fill", "Управление прокси…"),
      text("key", "Ctrl+P"),
    ),
  );
}

function proxyItem(ctx, p, { selected = false, fast = false, onpick }) {
  if (!p.available) {
    return h("div", { class: "menu-item proxy-item unavailable", role: "menuitem", "aria-disabled": "true" },
      h("span", { class: "ind" }),
      proxyDot(p.slot),
      h("div", { class: "text col fill" },
        h("div", { class: "title-row" }, text("title", proxyName(p.slot)), typeChip(p.type)),
        text("sub", `Недоступен · ${minutesSince(p.unavailableSince ?? ctx.now, ctx.now)} мин назад`),
      ),
      h("button", { class: "btn btn-subtle c-accentText", onclick: () => ctx.call("proxy.check", { proxyId: p.id }) },
        icon("arrow-sync-16", "c-accentText"), "Проверить"),
    );
  }
  return h("button", { class: `menu-item proxy-item${selected ? " selected" : ""}`, role: "menuitemradio", "aria-checked": String(selected), onclick: onpick },
    h("span", { class: "ind" }),
    proxyDot(p.slot),
    h("div", { class: "text col fill" },
      h("div", { class: "title-row" },
        text("title", proxyName(p.slot)),
        typeChip(p.type),
        fast && h("span", { class: "chip chip-fast" }, icon("flash-16", "c-success"), "быстрый"),
      ),
      text("sub ellipsis", [p.country, p.hostMasked].filter(Boolean).join(" · ")),
    ),
    pingView(p.pingMs),
    h("span", { class: "check" }, selected && icon("checkmark-circle-16")),
  );
}

function directItem(selected, onpick) {
  return h("button", { class: "menu-item direct-item", role: "menuitemradio", "aria-checked": String(selected), onclick: onpick },
    h("span", { class: "ind" }),
    icon("globe-16", "c-text2"),
    h("div", { class: "box col fill" }, text("title", "Напрямую"), text("sub ellipsis", "Без прокси — сайты видят ваш IP")),
    h("span", { class: "check" }, selected && icon("checkmark-circle-16")),
  );
}

// ---------- proxy down: switch suggestion (g) ----------

function suggestion(ctx, view) {
  const f = view.down.fastest;
  const checking = !!ctx.ui.checkingProxy;
  return h("div", { class: "suggest-wrap" },
    h("div", { class: "switch-suggestion pill-width" },
      h("div", { class: "box" },
        icon("flash-16", "c-success"),
        text("lead fill ellipsis", ["Быстрее всех: " + proxyName(f.proxy.slot), f.proxy.country].filter(Boolean).join(" · ")),
        pingView(f.pingMs),
      ),
      h("div", { class: "box actions" },
        retryButton(ctx, { kind: "secondary", tight: true, checking, onclick: () => ctx.checkProxy(view.down.proxyId) }),
        h("button", { class: "btn btn-accent fill", onclick: () => ctx.call("proxy.switchFastest") }, "Переключиться на быстрейший"),
      ),
    ),
  );
}

// ---------- applications ----------

function appSection(ctx, view) {
  const { state } = ctx;
  const allTraffic = state.enabled && !!view.others;
  return h("section", { class: "rules" },
    h("div", { class: "section-header h28" },
      text("label", "Приложения"),
      h("div", { class: "spacer" }),
      h("button", { class: "btn btn-subtle c-accentText", "data-key": "add", onclick: () => ctx.openAddApp() },
        icon("add-16", "c-accentText"), "Добавить"),
    ),
    h("div", { class: "app-list" },
      allTraffic && allAppsRow(state),
      allTraffic && text("shadow-note ellipsis", "Сейчас не влияет — через прокси идёт весь трафик"),
      state.apps.map((app) => appRow(ctx, app, view, { shadow: !state.enabled || allTraffic })),
    ),
  );
}

function allAppsRow(state) {
  const t = state.allTraffic || { downBps: 0, upBps: 0 };
  return h("div", { class: "app-row all" },
    h("div", { class: "line" },
      h("span", { class: "others-icon" }, icon("globe-16")),
      h("div", { class: "text col fill" },
        text("name ellipsis", "Все приложения Windows"),
        text("second speed", speed(t.downBps, t.upBps), { "data-speed": "*" }),
      ),
      statusPill("running"),
    ),
  );
}

export function appRow(ctx, app, view, { shadow = false, preview = false } = {}) {
  // TODO: insufficient data: no design for a paused row; it is drawn «В тени» as «идёт напрямую».
  const paused = app.status === "paused";
  const isShadow = shadow || paused;
  const status = isShadow ? null : app.status;
  const hover = !preview && ctx.ui.hoverAppId === app.id;
  const classes = ["app-row", isShadow && "shadow", status === "error" && "error", hover && "hover", !preview && "has-menu"];
  return h("div", {
    class: classes.filter(Boolean).join(" "), "data-app": app.id,
    tabindex: preview ? undefined : "0", "aria-label": preview ? undefined : app.name, "data-key": `row-${app.id}`,
  },
    h("div", { class: "line" },
      appIcon(app),
      h("div", { class: "text col fill" }, text("name ellipsis", app.name), secondLine(ctx, app, view, isShadow)),
      statusPill(status),
      !preview && h("button", {
        class: "btn btn-subtle icon-only more", "aria-label": "Действия", "aria-haspopup": "menu", "data-key": `more-${app.id}`,
        onclick: (e) => ctx.openRowMenu(app.id, e.currentTarget),
      }, icon("more-vertical-16", "c-text2")),
    ),
    status === "error" && h("div", { class: "error-panel" },
      text("detail", app.error?.detail || app.error?.short || ""),
      h("div", { class: "row" }, retryButton(ctx, { onclick: () => ctx.call("apps.check", { appId: app.id }) })),
    ),
  );
}

function secondLine(ctx, app, view, shadow) {
  const running = app.status !== "notRunning";
  if (shadow) {
    if (!running) return text("second", "не запущено");
    const live = ctx.state.enabled && app.status !== "paused";
    return live ? speedText(app) : text("second", "идёт напрямую");
  }
  switch (app.status) {
    case "running": return speedText(app);
    case "notRunning": return text("second ellipsis", "Запустите — пойдёт через прокси");
    case "error": return text("second danger ellipsis", app.error?.short || "");
    case "checking": return text("second ellipsis", view?.down
      ? "На паузе — ждём прокси"
      : `Проверяем соединение через ${proxyName(view?.current?.slot ?? 1)}…`);
    default: return null;
  }
}

const speedText = (app) => text("second speed", speed(app.downBps, app.upBps), { "data-speed": app.id });

// ⋯ row menu: exactly three items (design Row menu).
function rowMenu(ctx, view) {
  const app = ctx.state.apps.find((a) => a.id === ctx.ui.rowMenu.appId);
  if (!app) return null;
  const close = (fn) => () => { ctx.closeMenus(); fn(); };
  const othersOn = !!view.others;
  return h("div", { class: "flyout row-menu", role: "menu", "data-menu": "row", style: ctx.ui.rowMenu.style },
    // TODO: insufficient data: no «resume» wording; the same item switches a paused app back.
    h("button", { class: "menu-item", role: "menuitem", onclick: close(() => ctx.call("apps.pause", { appId: app.id, paused: app.status !== "paused" })) },
      icon("subtract-16", "c-text2"),
      h("div", { class: "box col fill" }, text("label", "Приостановить"), text("sub", "идёт напрямую, пока не включите")),
    ),
    h("button", { class: "menu-item", role: "menuitem", disabled: !othersOn, onclick: close(() => ctx.call("apps.direct", { appId: app.id })) },
      icon("arrow-right-16", "c-text2"),
      h("div", { class: "box col fill" }, text("label", "Пустить напрямую"), text("sub", "только в режиме «Всё остальное → через прокси»")),
    ),
    h("button", { class: "menu-item short", role: "menuitem", onclick: close(() => ctx.call("apps.remove", { appId: app.id })) },
      icon("delete-16", "c-danger"),
      h("div", { class: "box col fill" }, text("label danger", "Убрать из списка")),
    ),
  );
}

// ---------- first run (a) ----------

const sourceLabel = { running: "запущено", startMenu: "Пуск", programFiles: "Program Files" };

function firstRunList(ctx) {
  const added = new Set(ctx.state.apps.map((a) => a.exePath.toLowerCase()));
  // TODO: insufficient data: the design does not show apps added before the first proxy; they stay hidden.
  const items = (ctx.ui.suggestions || []).filter((s) => !added.has((s.exePath || "").toLowerCase()));
  return h("section", { class: "rules" },
    h("div", { class: "section-header" }, text("label", "Найдено на этом ПК")),
    h("div", { class: "app-list" }, items.map((s) => h("div", { class: "suggest" },
      appIcon(s),
      text("name fill ellipsis", s.name),
      text("source", sourceLabel[s.source] || ""),
      h("button", { class: "btn btn-standard square", "aria-label": `Добавить ${s.name}`, onclick: () => ctx.addApp(s.exePath) },
        icon("add-16", "c-text")),
    ))),
  );
}

// ---------- footer «Всё остальное →» ----------

function footer(ctx, view) {
  const { state } = ctx;
  const muted = view.empty || !state.enabled;
  const p = view.others;
  return h("footer", { class: `footer${muted ? " muted" : ""}` },
    p && h("div", { class: "ks-note" }, icon("shield-globe-16", "c-text3"), text("fill", "Прокси упадёт — интернет на паузе, IP не утечёт")),
    h("div", { class: "switch" },
      icon("globe-16", "c-text2"),
      text("lead", "Всё остальное →"),
      h("button", {
        class: `dropdown${p ? " on" : ""}`, disabled: muted, "aria-haspopup": "menu", "data-key": "others",
        onclick: () => ctx.toggleFooterMenu(),
      }, p ? proxyDot(p.slot, 8) : directDot(), p ? `через ${proxyName(p.slot)}` : "напрямую", icon("chevron-down-12", "c-text2")),
    ),
  );
}

// The footer drop-down lists the same items as the proxy picker: available
// proxies and «Напрямую». TODO: insufficient data: its position is not in the design.
function footerMenu(ctx, view) {
  const { state } = ctx;
  const target = view.others?.id || null;
  const pick = (proxyId) => {
    ctx.call("others.set", { target: proxyId ? { proxyId } : "direct" });
    ctx.closeMenus();
  };
  const proxies = state.proxies.filter((p) => p.available).sort((a, b) => a.slot - b.slot);
  const bottom = view.others ? 73 : 49; // footer height (69 or 45) + 4
  return h("div", { class: "flyout picker", role: "menu", "data-menu": "footer", style: `top:auto;bottom:${bottom}px` },
    proxies.map((p) => proxyItem(ctx, p, { selected: p.id === target, onpick: () => pick(p.id) })),
    h("div", { class: "divider" }),
    directItem(target === null, () => pick(null)),
  );
}
