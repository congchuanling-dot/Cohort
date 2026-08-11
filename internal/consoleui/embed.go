package consoleui

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var assets embed.FS

func Assets() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
