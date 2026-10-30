# Ashgate Lite

Per-application proxy client for Windows 10/11 x64. Chosen apps go through
one current proxy (SOCKS5, HTTP CONNECT or VLESS); everything else goes
direct, or through a proxy in «весь трафик» mode. If the proxy stops
answering, the apps' traffic is blocked instead of leaking (kill switch).

## Layout

| Path | What |
|---|---|
| `internal/proxycore/` | Proxy core from Ashgatev2: SOCKS5, HTTP CONNECT and VLESS (embedded Xray) clients, WinDivert capture engine, health check. The app uses it only through `proxycore.Core` (`Configure`, `Connect`, `Disconnect`, `Status`, `Stats`) and `proxycore.Ping` / `ParseLink`. |
| `internal/lite/` | Backend of the UI: `settings.json`, the bridge `State`, commands, proxy health, speeds. |
| `internal/server/` | Loopback HTTP bridge (`docs/bridge.md`). |
| `internal/discover/` | Running apps, Start menu and Program Files for suggestions. |
| `internal/host/` | Frameless WebView2 windows. |
| `ui-lite/` | The UI: vanilla HTML/CSS/JS from the design handoff (`ashgate-lite-claude-code.zip`). |
| `cmd/ashgate-lite/` | Entry point. |

## Build

Needs Go 1.25+. On Windows, double-click `build.bat`. Elsewhere (needs
`curl` and `unzip`):

```sh
bash scripts/build.sh   # dist/ashgate-lite.exe + WinDivert.dll + WinDivert64.sys
```

The exe asks for administrator rights (WinDivert) and needs the WebView2
Runtime, which Windows 10/11 ship.

## Develop

```sh
go test ./...                     # VLESS end-to-end tests run without -race
go run ./cmd/ashgate-lite         # outside Windows: prints the UI URL, capture is off
python3 -m http.server -d ui-lite 5173
# http://localhost:5173/?mock=b_glavnaya&theme=dark  — any design frame a…l
```

`python3 tools/gen_icons.py` rebuilds `ui-lite/js/icons.js` from `design/icons/`.
Open design questions are marked `TODO: insufficient data` in the code.

## Licenses

Embedded Xray-core is MPL-2.0; WinDivert (shipped next to the exe, not
modified) is LGPL-3.0; Manrope and IBM Plex Mono are OFL-1.1
(`ui-lite/fonts/`); icons are Fluent UI System Icons (MIT).
