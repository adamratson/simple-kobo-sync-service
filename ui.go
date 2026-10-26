package main

import (
	"embed"
	"io/fs"
)

// webDist holds the built React UI (web/dist). Run `npm run build` in web/ to
// populate it; without a build only a placeholder is embedded and the UI is off.
//
//go:embed all:web/dist
var webDist embed.FS

func webUI() fs.FS {
	dist, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		return nil
	}
	return uiFS(dist)
}
