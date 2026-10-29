// «Управление прокси» window: screens e (list) and k (edit one proxy).
import { h, icon, text } from "../dom.js";
import { minutesSince, proxyName } from "../format.js";
import { currentChip, errorText, pingView, proxyDot, retryButton, titleBar, typeChip } from "./common.js";

const MAX_PROXIES = 3;

export function renderManage(ctx) {
  const editing = ctx.state.proxies.find((p) => p.id === ctx.ui.editProxyId);
  return h("div", { class: "win" },
    titleBar(ctx, { secondary: true }),
    editing ? editBody(ctx, editing) : listBody(ctx),
    editing ? editFooter(ctx, editing) : listFooter(ctx),
  );
}

// ---------- e: proxy list ----------

function listBody(ctx) {
  const proxies = [...ctx.state.proxies].sort((a, b) => a.slot - b.slot);
  const full = proxies.length >= MAX_PROXIES;
  return h("div", { class: "body manage-body" },
    h("div", { class: "manage-head h28" },
      text("title", "Прокси"),
      h("span", { class: `chip chip-key${full ? " warn" : ""}` }, `${proxies.length} из ${MAX_PROXIES}`),
      h("div", { class: "spacer" }),
      h("button", { class: "btn btn-subtle c-text2", onclick: () => ctx.call("proxy.ping", {}) }, icon("arrow-sync-16", "c-text2"), "Проверить все"),
    ),
    full ? lockedField() : pasteField(ctx),
    h("div", { class: "proxy-list" }, proxies.map((p) => proxyCard(ctx, p))),
  );
}

function lockedField() {
  return h("div", { class: "textbox rest disabled" },
    h("div", { class: "content" },
      icon("lock-closed-16", "c-text2"),
      text("value muted", "3 из 3 — удалите прокси, чтобы вставить новый"),
    ),
    h("div", { class: "underline" }),
  );
}

// TODO: insufficient data: the design shows only the locked field (3 из 3);
// below the limit the same field accepts a pasted link.
function pasteField(ctx) {
  return h("label", { class: "textbox rest" },
    h("div", { class: "content" },
      icon("copy-16", "c-text2"),
      h("input", {
        type: "text", placeholder: "Скопируйте ссылку — она вставится сама", "aria-label": "Ссылка прокси",
        "data-key": "paste", spellcheck: "false",
        onkeydown: (e) => e.key === "Enter" && ctx.pasteProxy(e.target.value),
      }),
    ),
    h("div", { class: "underline" }),
  );
}

function proxyCard(ctx, p) {
  const current = p.id === ctx.state.currentProxyId;
  const down = !p.available;
  const place = p.country ? `${p.country} · ` : "";
  const card = h("div", { class: `proxy-card${down ? " error" : ""}` },
    h("div", { class: "box" },
      proxyDot(p.slot),
      text("name", proxyName(p.slot)),
      typeChip(p.type),
      current && currentChip(),
      h("div", { class: "spacer" }),
      down
        ? h("span", { class: "ping-err f-monoSpeed" }, icon("error-circle-16", "c-danger"), errorText(p.lastError))
        : pingView(p.pingMs),
    ),
    h("div", { class: "box actions" },
      text("addr fill ellipsis", `${place}${p.hostMasked}:${p.port}`),
      action("arrow-sync-16", "Проверить", () => ctx.checkProxy(p.id)),
      action("edit-16", "Изменить", () => ctx.editProxy(p.id)),
      action("copy-16", "Копировать ссылку", () => ctx.call("proxy.copy", { proxyId: p.id })),
      action("delete-16", "Удалить", () => ctx.call("proxy.remove", { proxyId: p.id }), "c-danger"),
    ),
  );
  if (!down) return card;
  const minutes = minutesSince(p.unavailableSince ?? ctx.now, ctx.now);
  return [card, h("div", { class: "retry-bar" },
    icon("error-circle-16", "c-danger"),
    text("msg fill ellipsis", `${proxyName(p.slot)} не отвечает уже ${minutes} мин`),
    retryButton(ctx, { kind: "secondary", checking: ctx.ui.checkingProxy === p.id, onclick: () => ctx.checkProxy(p.id) }),
  )];
}

function action(name, label, onclick, color = "c-text2") {
  return h("button", { class: "btn btn-subtle icon-only", "aria-label": label, title: label, onclick }, icon(name, color));
}

function listFooter(ctx) {
  return h("footer", { class: "manage-footer" },
    text("hint fill", "Ссылку можно вставить в любом окне — Ctrl+V"),
    h("button", { class: "btn btn-accent", "data-key": "done", onclick: () => ctx.call("window.close") }, "Готово"),
  );
}

// ---------- k: edit one proxy ----------

function editBody(ctx, p) {
  return h("div", { class: "body manage-body" },
    h("div", { class: "manage-head" },
      proxyDot(p.slot),
      text("title", proxyName(p.slot)),
      p.id === ctx.state.currentProxyId && currentChip(),
    ),
    text("edit-label", "Ссылка прокси"),
    h("label", { class: "textbox focused" },
      h("div", { class: "content" },
        h("input", {
          type: "text", value: ctx.ui.editLink || "", "aria-label": "Ссылка прокси", "data-key": "edit-link",
          spellcheck: "false", autofocus: true,
          oninput: (e) => ctx.setUI({ editLink: e.target.value }, false),
          onkeydown: (e) => e.key === "Enter" && ctx.saveProxy(),
        }),
      ),
      h("div", { class: "underline" }),
    ),
  );
}

function editFooter(ctx, p) {
  return h("footer", { class: "manage-footer" },
    h("button", { class: "btn btn-danger", onclick: () => ctx.removeEdited(p.id) }, icon("delete-16", "c-danger"), "Удалить"),
    h("div", { class: "spacer" }),
    h("button", { class: "btn btn-standard", onclick: () => ctx.closeEdit() }, "Отмена"),
    h("button", { class: "btn btn-accent", onclick: () => ctx.saveProxy() }, "Сохранить"),
  );
}
