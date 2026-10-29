// Package uilite embeds the Ashgate Lite web UI. The design mock mode
// (mock/) is left out of the binary.
package uilite

import "embed"

// FS holds index.html, css/, fonts/ and js/.
//
//go:embed index.html css fonts js
var FS embed.FS
