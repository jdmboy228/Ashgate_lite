//go:build windows

package host

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procMoveMemory       = kernel32.NewProc("RtlMoveMemory")
	errNoWebView         = errors.New("WebView2 Runtime не найден")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
)

// CopyText puts text on the Windows clipboard.
func CopyText(text string) error {
	u, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if r, _, err := procOpenClipboard.Call(0); r == 0 {
		return err
	}
	defer call(procCloseClipboard)
	call(procEmptyClipboard)
	size := uintptr(len(u) * 2)
	mem, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if mem == 0 {
		return err
	}
	ptr, _, err := procGlobalLock.Call(mem)
	if ptr == 0 {
		call(procGlobalFree, mem)
		return err
	}
	call(procMoveMemory, ptr, uintptr(unsafe.Pointer(&u[0])), size)
	call(procGlobalUnlock, mem)
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, mem); r == 0 {
		call(procGlobalFree, mem)
		return err
	}
	return nil // the clipboard owns mem now
}
