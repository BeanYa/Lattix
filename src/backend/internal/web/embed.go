// Package web exposes the release frontend bundled into the panel binary.
package web

import (
	"embed"
	"io/fs"
)

// Release and Docker builds copy the Vite output into dist before compiling.
// The committed .gitkeep keeps ordinary Go builds valid between frontend builds.
//
//go:embed all:dist
var embeddedDist embed.FS

// Dist returns the embedded frontend rooted at dist/.
func Dist() fs.FS {
	dist, err := fs.Sub(embeddedDist, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}
