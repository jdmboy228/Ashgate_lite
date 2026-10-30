//go:build !windows

package discover

// native finds nothing outside Windows: Ashgate Lite proxies Windows apps.
type native struct{}

func (native) running() []app   { return nil }
func (native) shortcuts() []app { return nil }
func (native) installed() []app { return nil }
