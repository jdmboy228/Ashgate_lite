package vless

import (
	// Server-side features used only by the test servers.
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/vless/inbound"
)

// StartRaw boots an Xray instance from a JSON document. Tests use it as the VLESS server.
func StartRaw(raw []byte) (func(), error) {
	inst, err := loadAndStart(raw)
	if err != nil {
		return nil, err
	}
	return func() { _ = inst.Close() }, nil
}
