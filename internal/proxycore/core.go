package proxycore

import (
	"context"
	"errors"
	"sync"

	"ashgate-lite/internal/proxycore/capture"
	"ashgate-lite/internal/proxycore/capture/process"
	"ashgate-lite/internal/proxycore/vless"
)

// Route sends one application, by full exe path, through a proxy. An empty
// ProxyID keeps the application direct, even when Config.Others is set.
type Route struct {
	ExePath string
	ProxyID string
}

// Config is what the core proxies and how.
type Config struct {
	Proxies map[string]Proxy // by proxy ID
	Apps    []Route
	// Others is the proxy for every application without a route; "" is direct.
	Others string
}

// Stat is the traffic of one route since Connect; see Stats.
type Stat = capture.Stat

// OthersKey is the Stats key of Config.Others.
const OthersKey = capture.OthersKey

// StatKey is the Stats key of a route's exe path.
func StatKey(exePath string) string { return process.NormalizePath(exePath) }

// Status reports whether traffic is being captured.
type Status struct {
	Connected bool
	// Err is why capture stopped by itself or why Connect failed.
	Err error
	// BridgeErrs lists VLESS proxies whose local bridge did not start, by ID.
	BridgeErrs map[string]error
}

// Core runs capture and the VLESS bridges. It is safe for concurrent use.
type Core struct {
	mu      sync.Mutex
	cfg     Config
	up      *upstreams
	bridges *vless.Manager
	sess    *capture.Session
	err     error
	brErrs  map[string]error
}

// New returns a disconnected core.
func New() *Core {
	return &Core{up: &upstreams{}}
}

// Configure installs cfg. While connected it takes effect at once; open
// TCP connections keep the proxy they started with.
func (c *Core) Configure(cfg Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
	if c.sess == nil {
		return nil
	}
	c.syncBridgesLocked()
	return c.sess.Apply(c.planLocked())
}

// Connect starts capturing the configured applications.
func (c *Core) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		return nil
	}
	c.bridges = vless.NewManager()
	c.syncBridgesLocked()
	sess, err := capture.Start(context.Background(), c.planLocked(), c.up)
	if err != nil {
		c.closeBridgesLocked()
		c.err = err
		return err
	}
	c.sess, c.err = sess, nil
	go c.watch(sess)
	return nil
}

// Disconnect stops capture and the VLESS bridges. Applications go direct.
func (c *Core) Disconnect() {
	c.mu.Lock()
	sess := c.sess
	c.sess = nil
	c.closeBridgesLocked()
	c.mu.Unlock()
	if sess != nil {
		sess.Stop()
		<-sess.Done()
	}
}

// Status reports the capture state.
func (c *Core) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Connected: c.sess != nil, Err: c.err, BridgeErrs: c.brErrs}
}

// Stats returns per-route traffic keyed by StatKey, plus OthersKey. It is
// empty while disconnected.
func (c *Core) Stats() map[string]Stat {
	c.mu.Lock()
	sess := c.sess
	c.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.Stats()
}

// watch records a capture loop that stopped by itself.
func (c *Core) watch(sess *capture.Session) {
	<-sess.Done()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != sess {
		return
	}
	c.sess = nil
	c.err = sess.Err()
	if c.err == nil {
		c.err = errors.New("перехват остановлен")
	}
	c.closeBridgesLocked()
}

// syncBridgesLocked starts a bridge for every VLESS proxy in use and
// publishes the proxies to the capture engine.
func (c *Core) syncBridgesLocked() {
	var items []vless.Endpoint
	for id, p := range c.cfg.Proxies {
		if p.Type == VLESS && p.VLESS != nil {
			items = append(items, vless.Endpoint{ID: id, Address: p.Address(), Params: *p.VLESS})
		}
	}
	status, _ := c.bridges.Sync(context.Background(), items)
	addrs := make(map[string]string, len(status))
	c.brErrs = nil
	for id, st := range status {
		if st.SOCKS != "" {
			addrs[id] = st.SOCKS
			continue
		}
		if c.brErrs == nil {
			c.brErrs = map[string]error{}
		}
		c.brErrs[id] = errors.New(st.Err)
	}
	c.up.set(c.cfg.Proxies, addrs)
}

func (c *Core) closeBridgesLocked() {
	if c.bridges != nil {
		c.bridges.Close()
		c.bridges = nil
	}
	c.up.set(c.cfg.Proxies, nil)
}

func (c *Core) planLocked() capture.Plan {
	plan := capture.Plan{Default: c.cfg.Others, CarriesUDP: map[string]bool{}}
	for id, p := range c.cfg.Proxies {
		plan.CarriesUDP[id] = p.CarriesUDP()
		plan.Bypass = append(plan.Bypass, p.Host)
	}
	for _, r := range c.cfg.Apps {
		plan.Rules = append(plan.Rules, capture.Rule{Path: r.ExePath, ProxyID: r.ProxyID})
	}
	return plan
}
