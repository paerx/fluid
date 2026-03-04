package ui

import (
	"embed"
	"io/fs"
)

//go:embed assets/* index.html styles.css app.js
var files embed.FS

func FS() fs.FS {
	return files
}
