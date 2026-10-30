// Screen f: «Добавить приложение», inside the main window.
import { h, icon, text } from "../dom.js";
import { appIcon, titleBar } from "./common.js";
import { appRow } from "./main-screen.js";

const sources = {
  running: ["apps-16", "Запущено"],
  startMenu: ["apps-16", "Пуск"],
  programFiles: ["folder-open-16", "Program Files"],
};

export function renderAddApp(ctx) {
  const { query = "", results = [], selectedIndex = 0 } = ctx.ui;
  const selected = results[selectedIndex];
  return h("div", { class: "win" },
    titleBar(ctx),
    h("div", { class: "subheader" },
      h("button", { class: "back", "aria-label": "Назад", onclick: () => ctx.closeAddApp() }, icon("chevron-up-16", "c-text")),
      text("title", "Добавить приложение"),
    ),
    h("div", { class: "body add-body" },
      h("label", { class: "textbox focused" },
        h("div", { class: "content" },
          icon("search-16", "c-text"),
          h("input", {
            type: "text", value: query, "aria-label": "Поиск приложения", "data-key": "search", autofocus: true,
            spellcheck: "false", oninput: (e) => ctx.searchApps(e.target.value),
          }),
        ),
        h("div", { class: "underline" }),
      ),
      text("found", `Найдено ${results.length}`),
      h("div", { class: "results", role: "listbox", "aria-label": "Результаты" },
        results.map((r, i) => result(ctx, r, i === selectedIndex, i)),
      ),
      selected && text("preview-label", "Так оно появится в списке:"),
      selected && appRow(ctx, { id: "preview", name: selected.name, exePath: selected.exePath, iconDataUrl: selected.iconDataUrl, status: "notRunning" }, null, { preview: true }),
      h("div", { class: "spacer" }),
      h("button", { class: "btn btn-accent wide", disabled: !selected, onclick: () => ctx.addSelected() },
        "Добавить", h("span", { class: "key" }, "Enter")),
    ),
  );
}

function result(ctx, r, selected, index) {
  const [iconName, label] = sources[r.source] || sources.programFiles;
  return h("button", {
    class: `result${selected ? " selected" : ""}`, role: "option", "aria-selected": String(selected),
    onclick: () => ctx.selectResult(index), ondblclick: () => ctx.addSelected(),
  },
    appIcon(r, 28),
    h("div", { class: "box col fill" }, text("name ellipsis", r.name), text("path ellipsis", r.exePath)),
    h("span", { class: "chip chip-source" }, icon(iconName, "c-text2"), label),
  );
}
