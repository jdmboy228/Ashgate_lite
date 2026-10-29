//go:build windows

// Package host shows the UI in frameless WebView2 windows: the main window
// (420×640, min 360×520) and «Управление прокси» (420×560).
package host

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	dwmapi               = windows.NewLazySystemDLL("dwmapi.dll")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtr = user32.NewProc("GetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procReleaseCapture   = user32.NewProc("ReleaseCapture")
	procSendMessage      = user32.NewProc("SendMessageW")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
	procFindWindow       = user32.NewProc("FindWindowW")
	procDwmSetAttribute  = dwmapi.NewProc("DwmSetWindowAttribute")
	procDwmExtendFrame   = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	windowProc           = windows.NewCallback(subclassProc)
	subclassed           sync.Map // hwnd -> *frame
)

const (
	gwlStyle    = ^uintptr(15) // -16
	gwlpWndProc = ^uintptr(3)  // -4

	wsPopup       = 0x80000000
	wsThickFrame  = 0x00040000
	wsMinimizeBox = 0x00020000
	wsSysMenu     = 0x00080000

	wmClose         = 0x0010
	wmNCCalcSize    = 0x0083
	wmNCLButtonDown = 0x00A1
	htCaption       = 2
	swHide          = 0
	swShow          = 5
	swMinimize      = 6
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpFrameChanged = 0x0020
	dwmaCornerPref  = 33
	dwmCornerRound  = 2
	baseDPI         = 96
	mainTitle       = "Ashgate Lite"
	manageTitle     = "Управление прокси — Ashgate Lite"
)

// hitTest maps a resize edge from the page to WM_NCHITTEST codes.
var hitTest = map[string]uintptr{
	"w": 10, "e": 11, "n": 12, "nw": 13, "ne": 14, "s": 15, "sw": 16, "se": 17,
}

// frame is the subclass state of one window.
type frame struct {
	prev uintptr
	// hideOnClose keeps «Управление прокси» alive: destroying any webview
	// window would end the shared message loop.
	hideOnClose bool
}

// Host owns the windows. Every window call runs on the UI thread.
type Host struct {
	url    func(string) string
	main   webview2.WebView
	manage webview2.WebView
}

// New returns a host for pages at url(window).
func New(url func(window string) string) *Host { return &Host{url: url} }

// Run opens the main window and pumps messages until it closes. It must
// run on the main OS thread.
func (h *Host) Run(ctx context.Context) error {
	h.main = newWindow(mainTitle, 420, 640, true)
	if h.main == nil {
		return errNoWebView
	}
	h.main.SetSize(scale(h.main, 360), scale(h.main, 520), webview2.HintMin)
	makeFrameless(h.main, 420, 640, true, false)
	h.main.Navigate(h.url("main"))
	go func() {
		<-ctx.Done()
		h.main.Dispatch(h.main.Terminate)
	}()
	h.main.Run()
	return nil
}

func newWindow(title string, w, height uint, center bool) webview2.WebView {
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "Ashgate Lite", "WebView2")
	return webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  dataDir,
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: title, Width: w, Height: height, Center: center,
		},
	})
}

// Minimize hides a window to the taskbar.
func (h *Host) Minimize(window string) {
	h.onUI(window, func(w webview2.WebView) { call(procShowWindow, hwnd(w), swMinimize) })
}

// Close ends the app from the main window and hides «Управление прокси».
func (h *Host) Close(window string) {
	if window == "manage" {
		h.onUI(window, func(w webview2.WebView) { call(procShowWindow, hwnd(w), swHide) })
		return
	}
	h.main.Dispatch(h.main.Terminate)
}

// OpenManage shows «Управление прокси», creating it the first time.
func (h *Host) OpenManage() {
	h.main.Dispatch(func() {
		if h.manage == nil {
			h.manage = newWindow(manageTitle, 420, 560, true)
			if h.manage == nil {
				return
			}
			makeFrameless(h.manage, 420, 560, false, true)
			h.manage.Navigate(h.url("manage"))
		}
		call(procShowWindow, hwnd(h.manage), swShow)
		call(procSetForeground, hwnd(h.manage))
	})
}

// StartDrag moves the window with the mouse, as a native title bar does.
func (h *Host) StartDrag(window string) {
	h.onUI(window, func(w webview2.WebView) { nonClientDrag(w, htCaption) })
}

// StartResize resizes the main window from one of its edges.
func (h *Host) StartResize(window, edge string) {
	code, ok := hitTest[edge]
	if !ok || window != "main" {
		return
	}
	h.onUI(window, func(w webview2.WebView) { nonClientDrag(w, code) })
}

func (h *Host) onUI(window string, fn func(webview2.WebView)) {
	h.main.Dispatch(func() {
		w := h.main
		if window == "manage" {
			w = h.manage
		}
		if w != nil {
			fn(w)
		}
	})
}

func nonClientDrag(w webview2.WebView, code uintptr) {
	call(procReleaseCapture)
	call(procSendMessage, hwnd(w), wmNCLButtonDown, code, 0)
}

// makeFrameless drops the native frame (the page draws its own TitleBar),
// asks Windows 11 for rounded corners, and sizes the window for its DPI.
func makeFrameless(w webview2.WebView, width, height int, resizable, hideOnClose bool) {
	hw := hwnd(w)
	style := uintptr(wsPopup | wsMinimizeBox | wsSysMenu)
	if resizable {
		style |= wsThickFrame
	}
	call(procSetWindowLongPtr, hw, gwlStyle, style)
	prev, _, _ := procGetWindowLongPtr.Call(hw, gwlpWndProc)
	subclassed.Store(hw, &frame{prev: prev, hideOnClose: hideOnClose})
	call(procSetWindowLongPtr, hw, gwlpWndProc, windowProc)
	pref := uint32(dwmCornerRound)
	call(procDwmSetAttribute, hw, dwmaCornerPref, uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
	margins := [4]int32{0, 0, 0, 1} // keeps the DWM shadow on a frameless window
	call(procDwmExtendFrame, hw, uintptr(unsafe.Pointer(&margins)))
	call(procSetWindowPos, hw, 0, 0, 0, uintptr(scale(w, width)), uintptr(scale(w, height)), swpNoMove|swpNoZOrder|swpFrameChanged)
}

func subclassProc(hw, msg, wp, lp uintptr) uintptr {
	v, ok := subclassed.Load(hw)
	if !ok {
		return 0
	}
	f := v.(*frame)
	switch {
	case msg == wmNCCalcSize && wp != 0:
		return 0 // the client area covers the whole window
	case msg == wmClose && f.hideOnClose:
		call(procShowWindow, hw, swHide)
		return 0
	}
	r, _, _ := procCallWindowProc.Call(f.prev, hw, msg, wp, lp)
	return r
}

// scale converts CSS pixels to device pixels for the window's monitor.
func scale(w webview2.WebView, px int) int {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd(w))
	if dpi == 0 {
		dpi = baseDPI
	}
	return px * int(dpi) / baseDPI
}

func hwnd(w webview2.WebView) uintptr { return uintptr(w.Window()) }

func call(p *windows.LazyProc, args ...uintptr) { _, _, _ = p.Call(args...) }

// AlreadyRunning brings an open Ashgate Lite window to the front and
// reports whether one exists.
func AlreadyRunning() bool {
	name, _ := windows.UTF16PtrFromString(`Local\AshgateLiteUI`)
	h, err := windows.CreateMutex(nil, false, name)
	if err == nil {
		instance = h // held for the life of the process
		return false
	}
	if h != 0 {
		windows.CloseHandle(h)
	}
	title, _ := windows.UTF16PtrFromString(mainTitle)
	if hw, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(title))); hw != 0 {
		call(procShowWindow, hw, swShow)
		call(procSetForeground, hw)
	}
	return true
}

var instance windows.Handle
