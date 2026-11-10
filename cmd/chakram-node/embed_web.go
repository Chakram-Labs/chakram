//go:build webembed

package main

import (
	"embed"
	"io/fs"
)

//go:embed webdist
var webDistFS embed.FS

func webUI() fs.FS {
	sub, err := fs.Sub(webDistFS, "webdist")
	if err != nil {
		return nil
	}
	return sub
}
