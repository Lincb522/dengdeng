// Package web embeds the built frontend (frontend/dist copied to ./dist).
// A placeholder index.html keeps the embed valid before the first build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
