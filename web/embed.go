// Package web предоставляет шлюзу собранные файлы интерфейса.
package web

import (
	"embed"
	"io/fs"
)

// Перед компиляцией Go следует собрать интерфейс, чтобы наполнить dist/.
//
//go:embed all:dist
var Files embed.FS

// Dist содержит файлы фронтенда без префикса dist/.
var Dist fs.FS

func init() {
	var err error
	Dist, err = fs.Sub(Files, "dist")
	if err != nil {
		panic("не удалось открыть встроенный web/dist: " + err.Error())
	}
}
