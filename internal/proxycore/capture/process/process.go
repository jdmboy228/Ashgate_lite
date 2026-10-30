// Package process maps local sockets to the processes that own them.
package process

import "strings"

// Process is the application that owns a socket.
type Process struct {
	PID  uint32
	Name string
	Path string
	// PPID and Started (creation time, Unix nanoseconds) identify the
	// parent. Zero means unknown.
	PPID    uint32
	Started int64
	// Parents is the ancestor chain, nearest first. Each element has no
	// Parents of its own.
	Parents []Process
}

// NormalizePath lower-cases a Windows path, unifies slashes, and strips the
// extended-length prefix.
func NormalizePath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), `"`)
	p = strings.ToLower(strings.ReplaceAll(p, "/", `\`))
	p = strings.TrimPrefix(p, `\??\`)
	return strings.TrimPrefix(p, `\\?\`)
}

// Base returns the lower-case file name of a Windows path.
func Base(p string) string {
	p = NormalizePath(p)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		p = p[i+1:]
	}
	return p
}
