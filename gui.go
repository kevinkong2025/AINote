//go:build windows || darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

/* ------------------------------------------------------------------
   启动策略
   1) 优先 WebView2 原生窗口；
   2) 启动前用「一次性子进程」探测 WebView2 是否真的能渲染。
      探测失败/超时就 kill 掉这个子进程 —— 绝不让一个半死的 WebView2
      留在后台（它曾在数小时后弹错误框，一点确定把整个应用带走）；
   3) 探测结果记在 data/gui-mode.txt，下次启动直接走可用模式，无需再等。
   手动覆盖：noteharness.exe -webview / -app-window
------------------------------------------------------------------ */

func appCacheDir() string {
	base := os.Getenv("APPDATA")
	if base == "" {
		base = os.Getenv("HOME")
	}
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "NoteHarness")
}

func prepareWebViewEnv() {
	_ = os.MkdirAll(filepath.Join(appCacheDir(), "webview"), 0o755)
	if os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS") == "" {
		_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-gpu --disable-dev-shm-usage")
	}
}

func guiModePath() string { return filepath.Join(dataDir(), "gui-mode.txt") }

func loadGUIMode() string {
	b, err := os.ReadFile(guiModePath())
	if err != nil {
		return ""
	}
	m := strings.TrimSpace(string(b))
	if m != "webview" && m != "app" {
		return ""
	}
	return m
}

func saveGUIMode(m string) {
	_ = os.MkdirAll(dataDir(), 0o755)
	_ = os.WriteFile(guiModePath(), []byte(m), 0o644)
}

func runGUI(srv *Server) {
	mode := loadGUIMode()
	switch {
	case *flagWebview:
		mode = "webview"
	case *flagAppWin:
		mode = "app"
	case mode == "":
		// 首次启动：探测一次并记住结果
		mode = "webview"
		if !webviewAvailable() {
			mode = "app"
		}
		saveGUIMode(mode)
	}

	if mode == "webview" {
		// 原生窗口跑在子进程里：Wails 在 WebView2 浏览器进程崩溃时会弹框并 os.Exit(-1)，
		// 父进程接管后自动切到独立应用窗口，整个应用不会被“一枪带走”。
		if runWebViewChild() {
			return // 用户正常关闭窗口
		}
		saveGUIMode("app")
	}

	url := startServer(srv, 0)
	runAppWindow(srv, url)
}

// runWebViewChild 以子进程承载 WebView2 原生窗口，返回 true 表示正常退出。
func runWebViewChild() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, "-webview-child")
	if err := cmd.Start(); err != nil || cmd.Process == nil {
		return false
	}
	return cmd.Wait() == nil
}

// runWebViewWindow 子进程入口：原生窗口 + 本地服务（供其它启动检测到本实例）。
func runWebViewWindow(srv *Server) {
	_ = startServer(srv, 0)
	prepareWebViewEnv()
	_ = wails.Run(&options.App{
		Title:     "NoteHarness - 提示词笔记库",
		Width:     1320,
		Height:    860,
		MinWidth:  1024,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Handler: buildMux(srv),
		},
		Windows: &windows.Options{
			WebviewUserDataPath:  filepath.Join(appCacheDir(), "webview"),
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Messages: &windows.Messages{
				WebView2ProcessCrash: "内置浏览器组件异常，NoteHarness 将切换为独立应用窗口重新启动。",
			},
		},
	})
	os.Exit(0)
}

// webviewAvailable 用一次性子进程探测 WebView2；超时/失败则彻底杀掉它。
func webviewAvailable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, "-webview-probe")
	if err := cmd.Start(); err != nil || cmd.Process == nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(20 * time.Second):
		killTree(cmd)
		return false
	}
}

// runWebViewProbe 子进程入口：加载真实页面，渲染成功并稳定 3 秒后退出 0，超时退出 3。
func runWebViewProbe(srv *Server) {
	prepareWebViewEnv()
	ready := make(chan struct{}, 1)
	go func() {
		_ = wails.Run(&options.App{
			Title:       "NoteHarness",
			Width:       400,
			Height:      300,
			StartHidden: true,
			AssetServer: &assetserver.Options{
				Handler: buildMux(srv),
			},
			OnDomReady: func(ctx context.Context) {
				select {
				case ready <- struct{}{}:
				default:
				}
			},
			Windows: &windows.Options{
				WebviewUserDataPath: filepath.Join(appCacheDir(), "webview"),
			},
		})
	}()
	select {
	case <-ready:
		// 再观察 3 秒：有些机器能渲染但浏览器进程随后就崩，这种情况直接判定为不可用
		time.Sleep(3 * time.Second)
		os.Exit(0)
	case <-time.After(25 * time.Second):
		os.Exit(3)
	}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F").Run()
		return
	}
	_ = cmd.Process.Kill()
}

// runAppWindow 用 Edge/Chrome 的 --app 模式开一个独立窗口（无地址栏、独立任务栏图标）。
// 窗口关闭后进程随之退出，不留后台僵尸。
func runAppWindow(srv *Server, url string) {
	fmt.Printf("NoteHarness 已启动: %s\n数据目录: %s\n", url, dataDir())
	cmd, ok := openAppWindow(url)
	if !ok || cmd == nil {
		openBrowser(url)
		select {}
	}
	start := time.Now()
	_ = cmd.Wait()
	if time.Since(start) < 3*time.Second {
		// 可能把 URL 交给了已有的浏览器进程，保持服务存活，避免窗口还在、进程却退了
		select {}
	}
}

var browserPaths = []string{
	`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
	`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
}

func openAppWindow(url string) (*exec.Cmd, bool) {
	// 使用独立的用户数据目录：浏览器进程才会常驻（能被 Wait 到），且不会串到日常浏览器
	profile := filepath.Join(appCacheDir(), "appwindow")
	_ = os.MkdirAll(profile, 0o755)
	for _, p := range browserPaths {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		args := []string{
			"--app=" + url,
			"--window-size=1320,860",
			"--user-data-dir=" + profile,
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-features=Translate",
		}
		cmd := exec.Command(p, args...)
		if err := cmd.Start(); err == nil {
			return cmd, true
		}
	}
	return nil, false
}
