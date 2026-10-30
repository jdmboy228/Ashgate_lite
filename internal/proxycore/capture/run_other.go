//go:build !windows

package capture

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by Start outside Windows.
var ErrUnsupported = errors.New("перехват трафика работает только в Windows 10/11 x64")

// Start reports that packet capture is only available on Windows.
func Start(context.Context, Plan, Upstream) (*Session, error) {
	return nil, ErrUnsupported
}
