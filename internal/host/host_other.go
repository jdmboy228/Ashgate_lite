//go:build !windows

// Package host shows the UI in native windows. Outside Windows it runs
// headless for development: open the printed URL in a browser.
package host

import (
	"context"
	"errors"
	"log"
)

// Host is a no-op window host.
type Host struct{ url func(string) string }

// New returns a host for pages at url(window).
func New(url func(window string) string) *Host { return &Host{url: url} }

// Run prints the page address and waits for ctx.
func (h *Host) Run(ctx context.Context) error {
	log.Printf("Ashgate Lite UI: %s", h.url("main"))
	<-ctx.Done()
	return nil
}

func (h *Host) Minimize(string)            {}
func (h *Host) Close(string)               {}
func (h *Host) StartDrag(string)           {}
func (h *Host) StartResize(string, string) {}

// OpenManage logs the manage window address.
func (h *Host) OpenManage() { log.Printf("Управление прокси: %s", h.url("manage")) }

// CopyText is not available without a desktop clipboard.
func CopyText(string) error {
	return errors.New("буфер обмена доступен только в Windows")
}

// AlreadyRunning reports another instance; there is none to find here.
func AlreadyRunning() bool { return false }
