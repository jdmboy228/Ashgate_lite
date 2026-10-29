//go:build windows

package discover

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type native struct{}

// running lists the exe of every process outside the Windows folder.
func (native) running() []app {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	self := uint32(os.Getpid())
	winDir := strings.ToLower(os.Getenv("WINDIR"))
	seen := map[string]bool{}
	var out []app
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if entry.ProcessID == 0 || entry.ProcessID == self {
			continue
		}
		path := imagePath(entry.ProcessID)
		low := strings.ToLower(path)
		if path == "" || seen[low] || (winDir != "" && strings.HasPrefix(low, winDir)) {
			continue
		}
		seen[low] = true
		out = append(out, app{name: exeName(path), path: path})
	}
	return out
}

func imagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// shortcuts resolves the .lnk files of both Start menus to their .exe targets.
func (native) shortcuts() []app {
	var out []app
	for _, root := range []string{
		filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs`),
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs`),
	} {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".lnk") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			target := linkTarget(data)
			if strings.EqualFold(filepath.Ext(target), ".exe") && exists(target) {
				out = append(out, app{name: exeName(path), path: target})
			}
			return nil
		})
	}
	return out
}

// installed lists executables up to three folders deep in Program Files,
// Program Files (x86) and %LOCALAPPDATA%\Programs.
func (native) installed() []app {
	var out []app
	for _, root := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs"),
	} {
		if root == "" {
			continue
		}
		base := strings.Count(filepath.Clean(root), `\`)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if strings.Count(path, `\`)-base >= 3 {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.EqualFold(filepath.Ext(path), ".exe") {
				out = append(out, app{name: exeName(path), path: path})
			}
			return nil
		})
	}
	return out
}

func exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}
