package lite

import (
	"context"
	"encoding/json"
	"fmt"
)

// CmdError is a command failure with a code the UI can act on.
type CmdError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CmdError) Error() string { return e.Code + ": " + e.Message }

func cmdErr(code, msg string) error { return &CmdError{Code: code, Message: msg} }

type handler func(s *Service, ctx context.Context, window string, args json.RawMessage) (any, error)

// commands is the command table of design docs/bridge-api.md, plus
// proxy.link and window.startResize (see docs/bridge.md).
var commands = map[string]handler{
	"state.get":    func(s *Service, _ context.Context, _ string, _ json.RawMessage) (any, error) { return s.State(), nil },
	"toggle.set":   withArgs(func(s *Service, a struct{ Enabled bool }) (any, error) { return nil, s.setEnabled(a.Enabled) }),
	"proxy.paste":  withArgs(func(s *Service, a struct{ Text string }) (any, error) { return s.pasteProxy(a.Text) }),
	"proxy.select": withArgs(func(s *Service, a struct{ ProxyID *string }) (any, error) { return nil, s.selectProxy(a.ProxyID) }),
	"proxy.ping": withCtx(func(s *Service, ctx context.Context, a struct{ ProxyID string }) (any, error) {
		go s.pingAll(context.WithoutCancel(ctx), a.ProxyID)
		return nil, nil
	}),
	"proxy.check": withCtx(func(s *Service, ctx context.Context, a struct{ ProxyID string }) (any, error) {
		return nil, s.checkProxy(ctx, a.ProxyID)
	}),
	"proxy.switchFastest": func(s *Service, _ context.Context, _ string, _ json.RawMessage) (any, error) {
		return nil, s.switchFastest()
	},
	"proxy.update": withArgs(func(s *Service, a struct{ ProxyID, Text string }) (any, error) {
		return s.updateProxy(a.ProxyID, a.Text)
	}),
	"proxy.remove": withArgs(func(s *Service, a struct{ ProxyID string }) (any, error) { return nil, s.removeProxy(a.ProxyID) }),
	"proxy.copy":   withArgs(func(s *Service, a struct{ ProxyID string }) (any, error) { return nil, s.copyProxy(a.ProxyID) }),
	"proxy.link":   withArgs(func(s *Service, a struct{ ProxyID string }) (any, error) { return s.proxyLink(a.ProxyID) }),
	"others.set":   withArgs(func(s *Service, a struct{ Target json.RawMessage }) (any, error) { return nil, s.setOthers(a.Target) }),
	"apps.suggest": withArgs(func(s *Service, a struct{ Query string }) (any, error) { return s.suggest(a.Query), nil }),
	"apps.add":     withArgs(func(s *Service, a struct{ ExePath string }) (any, error) { return s.addApp(a.ExePath) }),
	"apps.check": withCtx(func(s *Service, ctx context.Context, a struct{ AppID string }) (any, error) {
		return nil, s.checkApp(ctx, a.AppID)
	}),
	"apps.pause": withArgs(func(s *Service, a struct {
		AppID  string
		Paused bool
	}) (any, error) {
		return nil, s.pauseApp(a.AppID, a.Paused)
	}),
	"apps.direct":       withArgs(func(s *Service, a struct{ AppID string }) (any, error) { return nil, s.directApp(a.AppID) }),
	"apps.remove":       withArgs(func(s *Service, a struct{ AppID string }) (any, error) { return nil, s.removeApp(a.AppID) }),
	"settings.setTheme": withArgs(func(s *Service, a struct{ Theme string }) (any, error) { return nil, s.setTheme(a.Theme) }),
	"window.minimize":   windowCmd(func(h Host, w string) { h.Minimize(w) }),
	"window.close":      windowCmd(func(h Host, w string) { h.Close(w) }),
	"window.openManage": windowCmd(func(h Host, _ string) { h.OpenManage() }),
	"window.startDrag":  windowCmd(func(h Host, w string) { h.StartDrag(w) }),
	"window.startResize": func(s *Service, _ context.Context, w string, raw json.RawMessage) (any, error) {
		var a struct{ Edge string }
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		s.opt.Host.StartResize(w, a.Edge)
		return nil, nil
	},
}

// Handle runs one bridge command for window ("main" or "manage").
func (s *Service) Handle(ctx context.Context, window, cmd string, args json.RawMessage) (any, error) {
	h, ok := commands[cmd]
	if !ok {
		return nil, cmdErr("UNKNOWN", fmt.Sprintf("неизвестная команда %q", cmd))
	}
	return h(s, ctx, window, args)
}

func withArgs[A any](fn func(*Service, A) (any, error)) handler {
	return func(s *Service, _ context.Context, _ string, raw json.RawMessage) (any, error) {
		var a A
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return fn(s, a)
	}
}

func withCtx[A any](fn func(*Service, context.Context, A) (any, error)) handler {
	return func(s *Service, ctx context.Context, _ string, raw json.RawMessage) (any, error) {
		var a A
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		return fn(s, ctx, a)
	}
}

func windowCmd(fn func(Host, string)) handler {
	return func(s *Service, _ context.Context, w string, _ json.RawMessage) (any, error) {
		fn(s.opt.Host, w)
		return nil, nil
	}
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return cmdErr("BAD_ARGS", err.Error())
	}
	return nil
}
