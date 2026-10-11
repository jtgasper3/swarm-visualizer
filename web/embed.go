// Package web holds the frontend, so that it can be built into the server
// binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static returns the frontend's files.
func Static() fs.FS {
	static, err := fs.Sub(files, "static")
	if err != nil {
		// The directory is named in the embed directive above, so this cannot
		// fail at run time.
		panic(err)
	}
	return static
}
