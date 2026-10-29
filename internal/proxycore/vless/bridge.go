package vless

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	applog "github.com/xtls/xray-core/app/log"
	xraylog "github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"

	// Only the Xray features a Lite client bridge uses: a SOCKS5 inbound,
	// a VLESS outbound, and the transports and security a share link can name.
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/headers/http"
	_ "github.com/xtls/xray-core/transport/internet/headers/noop"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

func init() {
	// Xray's console logger is created per instance. Swap that creator for a
	// ring buffer so dial failures can be turned into Russian log lines.
	_ = applog.RegisterHandlerCreator(applog.LogType_Console, func(applog.LogType, applog.HandlerCreatorOptions) (xraylog.Handler, error) {
		return logHook{}, nil
	})
}

type logHook struct{}

func (logHook) Handle(msg xraylog.Message) {
	if msg == nil {
		return
	}
	pushLog(msg.String())
}

// Bridge is one local SOCKS5 inbound in front of a VLESS outbound.
type Bridge struct {
	Addr string
	inst *core.Instance
	stop chan struct{}
	once sync.Once
}

// Close stops the embedded Xray instance. It is safe to call more than once.
func (b *Bridge) Close() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		close(b.stop)
		if b.inst != nil {
			_ = b.inst.Close()
		}
	})
}

// Listen starts a SOCKS5 inbound on a random 127.0.0.1 port and a VLESS
// outbound to serverAddr. The bridge stops when ctx is cancelled or Close
// is called.
func Listen(ctx context.Context, serverAddr string, p Params) (*Bridge, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.Normalize(); err != nil {
		return nil, err
	}
	host, serverPort, err := splitServer(serverAddr)
	if err != nil {
		return nil, err
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		port, err := freePort()
		if err != nil {
			return nil, err
		}
		b, err := startBridge(ctx, host, serverPort, port, p)
		if err == nil {
			return b, nil
		}
		last = err
		if !bindConflict(err) {
			break
		}
	}
	if last == nil {
		last = errors.New("VLESS: не удалось запустить локальный мост")
	}
	return nil, humanizeStart(last)
}

func splitServer(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "", 0, errors.New("VLESS: неверный адрес сервера")
	}
	port, err := net.LookupPort("tcp", portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, errors.New("VLESS: неверный адрес сервера")
	}
	return host, port, nil
}

func startBridge(ctx context.Context, host string, serverPort, socksPort int, p Params) (*Bridge, error) {
	raw, err := ClientJSON("127.0.0.1", socksPort, host, serverPort, p)
	if err != nil {
		return nil, err
	}
	inst, err := loadAndStart(raw)
	if err != nil {
		return nil, err
	}
	b := &Bridge{Addr: fmt.Sprintf("127.0.0.1:%d", socksPort), inst: inst, stop: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			b.Close()
		case <-b.stop:
		}
	}()
	if err := waitPort(b.Addr, 2*time.Second); err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

// ClientJSON is the Xray client document for one VLESS outbound behind a
// SOCKS5 inbound (no auth, UDP on) at listen:socksPort.
func ClientJSON(listen string, socksPort int, serverHost string, serverPort int, p Params) ([]byte, error) {
	if err := p.Normalize(); err != nil {
		return nil, err
	}
	vuser := map[string]any{
		"id":         p.UUID,
		"encryption": "none",
	}
	if flow := p.DialFlow(); flow != "" {
		vuser["flow"] = flow
	}
	stream := map[string]any{
		"network":  p.Network,
		"security": p.Security,
	}
	switch p.Security {
	case "reality":
		stream["realitySettings"] = map[string]any{
			"serverName":  p.SNI,
			"fingerprint": p.Fingerprint,
			"publicKey":   p.PublicKey,
			"shortId":     p.ShortID,
			"spiderX":     p.SpiderX,
		}
	case "tls":
		tls := map[string]any{
			"serverName":    p.SNI,
			"fingerprint":   p.Fingerprint,
			"allowInsecure": p.AllowInsecure,
		}
		if p.ALPN != "" {
			tls["alpn"] = splitComma(p.ALPN)
		}
		stream["tlsSettings"] = tls
	}
	switch p.Network {
	case "xhttp":
		xs := map[string]any{}
		if p.Path != "" {
			xs["path"] = p.Path
		}
		if p.Host != "" {
			xs["host"] = p.Host
		}
		if p.Mode != "" {
			xs["mode"] = p.Mode
		}
		if len(p.Extra) > 0 {
			xs["extra"] = json.RawMessage(p.Extra)
		}
		stream["xhttpSettings"] = xs
	case "ws":
		ws := map[string]any{}
		if p.Path != "" {
			ws["path"] = p.Path
		}
		if p.Host != "" {
			ws["host"] = p.Host
		}
		stream["wsSettings"] = ws
	case "grpc":
		gs := map[string]any{"serviceName": p.ServiceName}
		if p.Authority != "" {
			gs["authority"] = p.Authority
		}
		if p.Mode == "multi" {
			gs["multiMode"] = true
		}
		stream["grpcSettings"] = gs
	case "httpupgrade":
		hs := map[string]any{}
		if p.Path != "" {
			hs["path"] = p.Path
		}
		if p.Host != "" {
			hs["host"] = p.Host
		}
		stream["httpupgradeSettings"] = hs
	default:
		if p.HeaderType != "" && !strings.EqualFold(p.HeaderType, "none") {
			stream["tcpSettings"] = map[string]any{
				"header": map[string]any{"type": p.HeaderType},
			}
		}
	}
	inbounds := []any{map[string]any{
		"listen":   listen,
		"port":     socksPort,
		"protocol": "socks",
		"settings": map[string]any{"auth": "noauth", "udp": true},
	}}
	doc := map[string]any{
		"log":      map[string]any{"loglevel": "info"},
		"inbounds": inbounds,
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": serverHost,
					"port":    serverPort,
					"users":   []any{vuser},
				}},
			},
			"streamSettings": stream,
		}},
	}
	return json.Marshal(doc)
}

func loadAndStart(raw []byte) (*core.Instance, error) {
	cfg, err := serial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	inst, err := core.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		return nil, err
	}
	return inst, nil
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func waitPort(addr string, d time.Duration) error {
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	if last == nil {
		last = errors.New("VLESS: локальный мост не открыл порт")
	}
	return last
}

func bindConflict(err error) bool {
	if err == nil {
		return false
	}
	low := strings.ToLower(err.Error())
	return strings.Contains(low, "bind") || strings.Contains(low, "address already in use") || strings.Contains(low, "only one usage")
}

func humanizeStart(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "Не удалось разобрать ссылку VLESS") || strings.HasPrefix(msg, "VLESS:") || strings.HasPrefix(msg, "REALITY:") {
		return err
	}
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "reality") && (strings.Contains(low, "public") || strings.Contains(low, "key") || strings.Contains(low, "short")):
		return errors.New("REALITY: рукопожатие не удалось. Проверьте публичный ключ, short id и SNI")
	case strings.Contains(low, "timeout") || strings.Contains(low, "deadline"):
		return ErrTimeout
	default:
		return fmt.Errorf("VLESS: не удалось запустить локальный мост: %s", msg)
	}
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
