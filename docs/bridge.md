# Bridge: implementation notes

The contract is the design handoff's `docs/bridge-api.md`. This file lists
only what the implementation adds or decides.

## Transport

- The backend serves `ui-lite/` on `127.0.0.1:<random port>`.
- `POST /api/cmd` with `{id, cmd, args}`; headers `X-Ashgate-Token` (per
  launch, from the page URL) and `X-Ashgate-Window: main|manage`.
- `GET /api/events?token=…` is a Server-Sent Events stream of
  `{event, data}`: `state` (coalesced to ≤ 10/s), `speeds` (1/s while on),
  `ping`, `appStatus`.
- Requests with any other `Host` are refused. `index.html` is served with
  the saved theme already in `data-theme`.

## Added commands

These are missing from the contract but needed by the screens:

| cmd | args | Why |
|---|---|---|
| `proxy.link` | `{ proxyId }` | k: the edit field is filled with the proxy's link. |
| `window.startResize` | `{ edge: n\|s\|e\|w\|ne\|nw\|se\|sw }` | The frameless main window resizes between 360×520 and its default 420×640. |

## Error codes

`PROXY_LIMIT`, `PROXY_PARSE` (contract), plus `PROXY_UNAVAILABLE`,
`NO_PROXY`, `NO_FASTEST`, `NOT_ALL_TRAFFIC`, `APP_EXISTS`, `BAD_APP`,
`NOT_FOUND`, `CAPTURE`, `CLIPBOARD`, `BAD_ARGS`, `UNKNOWN`, `INTERNAL`.
