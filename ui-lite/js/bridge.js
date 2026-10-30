// Transport for the Go ↔ JS contract (design docs/bridge-api.md).
// The backend serves this page with a per-launch token; commands go over
// HTTP POST and events arrive as Server-Sent Events.

export function liveBridge(onEvent) {
  const params = new URLSearchParams(location.search);
  const token = params.get("token") || "";
  const win = params.get("window") === "manage" ? "manage" : "main";
  const events = new EventSource(`/api/events?token=${encodeURIComponent(token)}`);
  events.onmessage = (e) => onEvent(JSON.parse(e.data));
  let seq = 0;
  return {
    async call(cmd, args = {}) {
      const id = `c-${++seq}`;
      const res = await fetch("/api/cmd", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Ashgate-Token": token, "X-Ashgate-Window": win },
        body: JSON.stringify({ id, cmd, args }),
      });
      const msg = await res.json();
      if (!msg.ok) throw Object.assign(new Error(msg.error?.message || cmd), { code: msg.error?.code });
      return msg.result;
    },
  };
}

// The ?mock= bridge renders a fixed design state; only the theme changes.
export function mockBridge(onEvent, state) {
  return {
    async call(cmd, args = {}) {
      if (cmd === "settings.setTheme") onEvent({ event: "state", data: { ...state, theme: args.theme } });
      return null;
    },
  };
}
