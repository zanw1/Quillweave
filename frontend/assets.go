package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var bundled embed.FS

// Assets embeds the same production frontend for desktop and preview runtimes.
func Assets() fs.FS {
	assets, err := fs.Sub(bundled, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}
