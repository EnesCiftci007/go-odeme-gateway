// Package web, ön yüz dosyalarını ve API dokümanını ikili dosyanın içine gömer.
// Böylece sunucu, çalıştığı klasörden bağımsız olarak tek bir dosya halinde dağıtılabilir.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var statik embed.FS

//go:embed openapi.yaml
var OpenAPI []byte

// Statik, ön yüz dosyalarını döndürür (kök: static/).
func Statik() (fs.FS, error) {
	return fs.Sub(statik, "static")
}
