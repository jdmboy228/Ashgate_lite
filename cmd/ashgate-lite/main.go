// Command ashgate-lite is the Ashgate Lite per-app proxy client for Windows.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"

	"ashgate-lite/internal/discover"
	"ashgate-lite/internal/host"
	"ashgate-lite/internal/lite"
	"ashgate-lite/internal/proxycore"
	"ashgate-lite/internal/server"
	uilite "ashgate-lite/ui-lite"
)

func init() {
	// The WebView2 windows and their message loop must stay on the main thread.
	runtime.LockOSThread()
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if host.AlreadyRunning() {
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	path, err := lite.SettingsPath()
	if err != nil {
		return err
	}
	var srv *server.Server
	win := host.New(func(window string) string { return srv.URL(window) })
	svc, err := lite.New(lite.Options{
		SettingsPath: path,
		Core:         proxycore.New(),
		Discover:     discover.New(),
		Host:         win,
		CopyText:     host.CopyText,
	})
	if err != nil {
		return err
	}
	srv = server.New(svc, uilite.FS)
	if err := srv.Start(ctx); err != nil {
		return err
	}
	svc.Start(ctx)
	defer svc.Shutdown()
	return win.Run(ctx)
}
