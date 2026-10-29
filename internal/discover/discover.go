// Package discover finds applications on this PC for «Найдено на этом ПК»
// and «Добавить приложение»: running processes, then Start menu shortcuts,
// then Program Files (design behavior.md §7).
package discover

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ashgate-lite/internal/lite"
	"ashgate-lite/internal/proxycore"
)

// Sources in priority order.
const (
	sourceRunning      = "running"
	sourceStartMenu    = "startMenu"
	sourceProgramFiles = "programFiles"
)

const (
	maxResults  = 20
	maxFirstRun = 8
	rescanEvery = 5 * time.Minute
)

// app is one executable found on disk or running.
type app struct {
	name string
	path string
}

// platform lists applications; it is implemented per OS.
type platform interface {
	running() []app
	shortcuts() []app
	installed() []app
}

// Finder implements lite.Discoverer.
type Finder struct {
	os platform

	mu        sync.Mutex
	scannedAt time.Time
	menu      []app
	files     []app
}

// New returns a finder for this OS.
func New() *Finder { return &Finder{os: native{}} }

// Running returns the exe paths of running processes, keyed by proxycore.StatKey.
func (f *Finder) Running() map[string]bool {
	out := map[string]bool{}
	for _, a := range f.os.running() {
		out[proxycore.StatKey(a.path)] = true
	}
	return out
}

// Suggest matches query against application names. An empty query is the
// first-run list: running apps and Start menu shortcuts only.
func (f *Finder) Suggest(query string) []lite.Suggestion {
	menu, files := f.scanned()
	q := strings.ToLower(strings.TrimSpace(query))
	groups := []struct {
		source string
		apps   []app
	}{{sourceRunning, f.os.running()}, {sourceStartMenu, menu}}
	limit := maxFirstRun
	if q != "" {
		groups = append(groups, struct {
			source string
			apps   []app
		}{sourceProgramFiles, files})
		limit = maxResults
	}
	seen := map[string]bool{}
	var out []lite.Suggestion
	for _, g := range groups {
		for _, a := range sortedByName(g.apps) {
			key := proxycore.StatKey(a.path)
			if seen[key] || !strings.Contains(strings.ToLower(a.name), q) {
				continue
			}
			seen[key] = true
			out = append(out, lite.Suggestion{Name: a.name, ExePath: a.path, Source: g.source})
			if len(out) == limit {
				return out
			}
		}
	}
	return out
}

// scanned returns the Start menu and Program Files lists, rescanning them
// when they are older than rescanEvery.
func (f *Finder) scanned() ([]app, []app) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Since(f.scannedAt) > rescanEvery {
		f.menu, f.files, f.scannedAt = f.os.shortcuts(), f.os.installed(), time.Now()
	}
	return f.menu, f.files
}

func sortedByName(apps []app) []app {
	out := append([]app(nil), apps...)
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].name) < strings.ToLower(out[j].name) })
	return out
}

// exeName is a display name from an exe path: «Telegram» for Telegram.exe.
func exeName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
