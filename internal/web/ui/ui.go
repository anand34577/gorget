// Package ui embeds the built web console (web/ → internal/web/ui/dist).
package ui

import "embed"

// Files holds the production build of the React console.
//
//go:embed all:dist
var Files embed.FS
