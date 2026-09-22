//go:build !windows && !darwin

package main

import (
	"os"
	"os/exec"
)

// Linux 等平台：Wails 的原生窗口需要 cgo + webkit2gtk-4.0 开发库，
// 交叉编译环境下不引入该依赖，直接以本地服务模式启动并打开系统浏览器。
// 若希望 Linux 下也用原生窗口，请在装好 webkit2gtk 的机器上执行 `wails build`。

func runGUI(srv *Server) {
	runHTTPServer(srv, 0)
}

func runWebViewProbe(srv *Server) { os.Exit(3) }

func runWebViewWindow(srv *Server) { runHTTPServer(srv, 0) }

func openAppWindow(url string) (*exec.Cmd, bool) { return nil, false }
