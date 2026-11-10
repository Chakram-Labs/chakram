//go:build !webembed

package main

import "io/fs"

// webUI returns nil when built without the embedded web UI.
// The API still works; the browser is opened to the public explorer instead.
func webUI() fs.FS { return nil }
