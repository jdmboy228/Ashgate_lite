// Formatting rules from design docs/bridge-api.md and docs/copy.md.

const KB = 1024;
const MB = 1024 * 1024;

// 1 приложение, 2–4 приложения, 5+ приложений (11–14 → many).
export function plural(n, one, few, many) {
  const tens = n % 100;
  const last = n % 10;
  if (tens >= 11 && tens <= 14) return many;
  if (last === 1) return one;
  if (last >= 2 && last <= 4) return few;
  return many;
}

// КБ/с up to 1000 КБ/с, then МБ/с with one decimal and a comma.
export function rate(bps) {
  const kb = Math.round((bps || 0) / KB);
  if (kb < 1000) return `${kb} КБ/с`;
  return `${(bps / MB).toFixed(1).replace(".", ",")} МБ/с`;
}

// "↓ 1,4 МБ/с   ↑ 120 КБ/с" — three spaces between the parts.
export function speed(down, up) {
  return `↓ ${rate(down)}   ↑ ${rate(up)}`;
}

export const ping = (ms) => `${ms} мс`;

export function minutesSince(ms, now = Date.now()) {
  return Math.max(1, Math.floor((now - ms) / 60000));
}

export function secondsSince(ms, now = Date.now()) {
  return Math.max(0, Math.round((now - ms) / 1000));
}

export const proxyName = (slot) => `Прокси ${slot}`;
