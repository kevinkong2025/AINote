package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

var (
	flagServe   = flag.Bool("serve", false, "以本地服务模式运行（用浏览器访问，适合 Linux/无桌面环境）")
	flagPort    = flag.Int("port", 0, "服务模式端口号（默认自动选择 5177-5199 中的空闲端口）")
	flagWebview = flag.Bool("webview", false, "强制使用 WebView2 原生窗口")
	flagAppWin  = flag.Bool("app-window", false, "强制使用独立应用窗口（Edge/Chrome --app 模式）")
	flagProbe   = flag.Bool("webview-probe", false, "内部使用：探测本机 WebView2 是否可用")
	flagChild   = flag.Bool("webview-child", false, "内部使用：以子进程承载 WebView2 原生窗口")
)

func dataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data"
	}
	return filepath.Join(filepath.Dir(exe), "data")
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func buildMux(srv *Server) http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.RegisterAPI(mux)
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

func main() {
	flag.Parse()

	// 子进程模式：先建好数据层，用真实页面验证 WebView2 能否稳定渲染
	if *flagProbe {
		st, err := NewStore(filepath.Join(dataDir(), "store.json"))
		if err != nil {
			os.Exit(3)
		}
		runWebViewProbe(NewServer(st))
		return
	}

	// 单实例：已有实例在跑就直接打开它的窗口，绝不启动第二份（避免两个进程各持一份数据互相覆盖）
	if !*flagServe {
		if url := findRunningInstance(); url != "" {
			revealExisting(url)
			return
		}
	}

	store, err := NewStore(filepath.Join(dataDir(), "store.json"))
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	srv := NewServer(store)

	if *flagChild {
		runWebViewWindow(srv)
		return
	}
	if *flagServe {
		runHTTPServer(srv, *flagPort)
		return
	}
	runGUI(srv)
}

// findRunningInstance 扫描常用端口，找到正在运行的 NoteHarness 实例。
func findRunningInstance() string {
	for p := 5177; p <= 5186; p++ {
		url := fmt.Sprintf("http://127.0.0.1:%d", p)
		if isOurServer(url) {
			return url
		}
	}
	return ""
}

var probeClient = &http.Client{
	Timeout: 400 * time.Millisecond,
	// 必须绕过系统代理，否则 127.0.0.1 会被代理拦截
	Transport: &http.Transport{Proxy: nil},
}

func isOurServer(url string) bool {
	resp, err := probeClient.Get(url + "/api/state")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	return strings.Contains(string(buf[:n]), "\"notes\"")
}

// revealExisting 复用已运行的实例：把它的地址开成一个新窗口即可，本进程随即退出。
func revealExisting(url string) {
	if _, ok := openAppWindow(url); ok {
		return
	}
	openBrowser(url)
}

func freePort(preferred int) int {
	if preferred > 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(preferred))
		if err == nil {
			_ = ln.Close()
			return preferred
		}
	}
	for cand := 5177; cand < 5200; cand++ {
		ln, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(cand))
		if err == nil {
			_ = ln.Close()
			return cand
		}
	}
	return 5177
}

// startServer 在同进程内启动本地服务（仅监听 127.0.0.1），返回访问地址。
func startServer(srv *Server, port int) string {
	p := freePort(port)
	addr := "127.0.0.1:" + fmt.Sprint(p)
	go func() {
		if err := http.ListenAndServe(addr, buildMux(srv)); err != nil {
			log.Fatal(err)
		}
	}()
	for i := 0; i < 100; i++ {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "http://" + addr
}

func runHTTPServer(srv *Server, port int) {
	url := startServer(srv, port)
	fmt.Printf("NoteHarness 服务模式已启动: %s\n数据目录: %s\n按 Ctrl+C 退出\n", url, dataDir())
	go func() {
		time.Sleep(600 * time.Millisecond)
		openBrowser(url)
	}()
	select {}
}
