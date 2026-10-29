package lite

// JSON shapes of design docs/bridge-api.md.

type proxyView struct {
	ID               string `json:"id"`
	Slot             int    `json:"slot"`
	Type             string `json:"type"`
	Country          string `json:"country"`
	HostMasked       string `json:"hostMasked"`
	Port             uint16 `json:"port"`
	PingMs           *int64 `json:"pingMs"`
	Available        bool   `json:"available"`
	UnavailableSince int64  `json:"unavailableSince,omitempty"`
	LastError        string `json:"lastError,omitempty"`
}

type appStatus string

const (
	statusRunning    appStatus = "running"
	statusNotRunning appStatus = "notRunning"
	statusError      appStatus = "error"
	statusChecking   appStatus = "checking"
	statusPaused     appStatus = "paused"
)

type appError struct {
	Short  string `json:"short"`
	Detail string `json:"detail"`
}

type appView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ExePath     string    `json:"exePath"`
	IconDataURL string    `json:"iconDataUrl,omitempty"`
	Status      appStatus `json:"status"`
	Error       *appError `json:"error,omitempty"`
	DownBps     int64     `json:"downBps"`
	UpBps       int64     `json:"upBps"`
}

type rates struct {
	DownBps int64 `json:"downBps"`
	UpBps   int64 `json:"upBps"`
}

type othersTarget struct {
	ProxyID string `json:"proxyId"`
}

type fastest struct {
	ProxyID string `json:"proxyId"`
	PingMs  int64  `json:"pingMs"`
}

type proxyDown struct {
	ProxyID string   `json:"proxyId"`
	SinceMs int64    `json:"sinceMs"`
	Fastest *fastest `json:"fastest,omitempty"`
}

// State is everything a window draws.
type State struct {
	Enabled        bool        `json:"enabled"`
	CurrentProxyID *string     `json:"currentProxyId"`
	Proxies        []proxyView `json:"proxies"`
	Apps           []appView   `json:"apps"`
	Others         any         `json:"others"` // "direct" or othersTarget
	AllTraffic     *rates      `json:"allTraffic,omitempty"`
	ProxyDown      *proxyDown  `json:"proxyDown,omitempty"`
	Theme          string      `json:"theme"`
}

// Suggestion is one application found on this PC (screens a and f).
type Suggestion struct {
	Name        string `json:"name"`
	ExePath     string `json:"exePath"`
	IconDataURL string `json:"iconDataUrl,omitempty"`
	Source      string `json:"source"` // running, startMenu, programFiles
}

type speedsEvent struct {
	Apps       map[string]rates `json:"apps"`
	AllTraffic *rates           `json:"allTraffic,omitempty"`
}

type pingEvent struct {
	ProxyID   string `json:"proxyId"`
	PingMs    *int64 `json:"pingMs"`
	Available bool   `json:"available"`
}

type appStatusEvent struct {
	AppID  string    `json:"appId"`
	Status appStatus `json:"status"`
	Error  *appError `json:"error,omitempty"`
}
