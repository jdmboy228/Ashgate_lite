// Tiny DOM builder: h("div", {class: "row", onclick}, child, "text").
import { icons } from "./icons.js";

export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (value === undefined || value === null || value === false) continue;
    if (key.startsWith("on")) el.addEventListener(key.slice(2), value);
    else if (key === "class") el.className = value;
    else if (key === "style") el.style.cssText = value;
    else el.setAttribute(key, value === true ? "" : value);
  }
  append(el, children);
  return el;
}

function append(el, children) {
  for (const child of children) {
    if (child === undefined || child === null || child === false) continue;
    if (Array.isArray(child)) append(el, child);
    else el.append(child instanceof Node ? child : String(child));
  }
}

// Text node in a block: text("f-name c-text ellipsis", "Chrome").
export function text(cls, content, attrs) {
  return h("div", { class: `t ${cls}`, ...attrs }, content);
}

// Inline SVG icon from design/icons; color comes from the cls color class.
export function icon(name, cls = "") {
  const size = name.match(/-(\d+)$/)?.[1] || "16";
  const el = h("span", { class: `ic ic-${size} ${cls}`, "aria-hidden": "true" });
  el.innerHTML = icons[name];
  return el;
}
