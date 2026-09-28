// Package web 承载前端（Vue 3 + Vite）的构建产物。
//
// 约定：前端执行 npm run build 后产物位于 web/dist，
// 通过 go:embed 打包进二进制，实现单文件部署。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// Dist 返回去掉 dist 前缀的静态资源文件系统。
func Dist() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}
