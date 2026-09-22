# NoteHarness Source Build & Run SOP (Windows 10)

> 适用对象：把本源码包拿到**另一台 Windows 10 机器**上，从零编译并运行 NoteHarness。
> 当前源码对应 `go.mod`：`go 1.27.1` + `github.com/wailsapp/wails/v2 v2.10.2`。

---

## 0. 交付物清单

源码包 `noteharness-src.zip` 解压后得到 `noteharness/` 目录，包含：

| 类型 | 文件 |
|------|------|
| 后端（Go） | `main.go`、`gui.go`、`gui_other.go`、`api.go`、`store.go`、`llm.go` |
| 前端（内嵌） | `static/index.html`、`static/app.js`、`static/style.css`、`static/marked.min.js` |
| 依赖描述 | `go.mod`、`go.sum` |
| 开发辅助 | `_fake_llm.py`（本地假 LLM 服务端，用于无真实 Key 时联调） |
| 文档 | `README.md`、本 SOP |

**已排除**（不会进包）：`*.exe` 二进制、`data/` 运行时数据、`probe.txt`/`shot-*.png` 测试产物。

---

## 1. 环境准备（目标 Win10 机器）

### 1.1 安装 Go（必须 ≥ 1.27.1）

1. 到 https://go.dev/dl/ 下载 **go1.27.1.windows-amd64.msi**（或更新的 1.27.x）。
2. 双击安装，默认会装到 `C:\Program Files\Go\` 并把 `C:\Program Files\Go\bin` 加入系统 PATH。
3. 打开 **PowerShell**，验证：
   ```powershell
   go version
   # 期望输出：go version go1.27.1 windows/amd64
   ```
   - 若提示 `go: 无法将“go”识别为...`：手动把 Go 的 `bin` 目录加进 PATH，或重开一个终端。
   - **无管理员权限也没关系**：可把 Go 装到用户目录（如 `%LOCALAPPDATA%\Programs\Go`），只要 `go` 命令能跑即可。

### 1.2 WebView2 运行时（可选，会自动降级）

- 程序**首次启动会自动探测**本机 WebView2 能否稳定渲染；若不可用，会**自动降级**为 Edge/Chrome 的 `--app` 独立窗口（功能完全一致，只是换了个浏览器内核）。
- 想要原生窗口体验，建议装 **Microsoft Edge WebView2 运行时**（Evergreen Bootstrapper，官网可下）；有 Chrome 也行。
- 该步骤**非强制**——哪怕什么都不装，程序也能用独立窗口模式跑起来。

### 1.3 网络

- **编译阶段**需要联网下载 Go 依赖（首次 `go build` 会拉取 wails 等模块，耗时 1–3 分钟）。
- **运行阶段**不需要联网，除非你要真正调用 LLM（那时需要能访问你配置的 Base URL）。
- 国内拉依赖慢可设置代理：
  ```powershell
  go env -w GOPROXY=https://goproxy.cn,direct
  ```

---

## 2. 解压源码

把 `noteharness-src.zip` 解压，得到 `noteharness/` 目录，后续命令均在该目录内执行：

```powershell
cd noteharness
```

---

## 3. 编译（Windows）

### 3.1 标准 Windows GUI 版（推荐）

用 **PowerShell** 或 **CMD** 执行：

```powershell
cd noteharness
go build -tags production -ldflags "-H windowsgui -s -w" -o noteharness.exe .
```

> ⚠️ **必须带 `-tags production`**，否则 Wails 会报：
> `Wails applications will not build without the correct build tags...`
>
> `-H windowsgui` 让程序运行时不弹黑色控制台窗口；`-s -w` 去掉调试符号、减小体积。

编译成功后会得到 `noteharness.exe`（约 12 MB）。

### 3.2 常见问题

| 现象 | 原因 / 解决 |
|------|------|
| `go: command not found` | Go 没加进 PATH，见 1.1 |
| `go.mod requires go >= 1.27.1` | 本机 Go 太旧，升级到 1.27.1+ |
| 依赖下载卡住 / 超时 | 设 `GOPROXY=https://goproxy.cn,direct`（1.3） |
| 报 `missing build tags` | 忘了 `-tags production`，重跑 3.1 |
| 杀软误报 / 拦截 exe | 把编译产物加白名单（无控制台窗口的 GUI 程序易被个别杀软敏感） |

### 3.3 交叉编译 Linux 版（可选，无需目标机环境）

```powershell
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -tags production -ldflags "-s -w" -o noteharness-linux .
```

> Linux 下原生窗口需要 cgo + webkit2gtk 开发库，故 `gui_other.go` 会自动降级为「服务模式」（见 4.2）。

---

## 4. 运行

### 4.1 图形界面（默认，双击即可）

```powershell
# 在 noteharness 目录下
.\noteharness.exe
```

- **首次启动**会用一次性子进程探测 WebView2（约 5–20 秒），结果写入 `data/gui-mode.txt`，之后秒开。
- **单实例**：若已有一个实例在跑，再双击只会给它开一个新窗口，不会启动第二份进程（避免两份数据互相覆盖）。
- 关闭窗口即退出，不留后台进程、不占端口。

### 4.2 服务模式（无桌面 / Linux / 远程）

```powershell
.\noteharness.exe -serve
# 或指定端口
.\noteharness.exe -serve -port 8080
```

浏览器访问 `http://127.0.0.1:<端口>` 即可使用。

### 4.3 强制窗口模式（排查用）

```powershell
.\noteharness.exe -app-window   # 强制用 Edge/Chrome 独立窗口
.\noteharness.exe -webview      # 强制用 WebView2 原生窗口
```

白屏/崩溃排查时，可删掉 `data/gui-mode.txt` 让它重新探测，或直接加 `-app-window`。

---

## 5. 数据与备份

- 所有数据保存在 **exe 同级的 `data/store.json`**（单文件 JSON）。
- 备份 = 直接拷贝 `data/` 目录；迁移到新机器时把 `data/` 一起带上即可。
- `store.json` 若损坏，程序会自动备份为 `*.corrupt-*` 并以空库启动，不会崩。

---

## 6. 使用前必做：配置 LLM

1. 顶栏点 **⚙（设置）** → 添加模型配置：别名 / Base URL / 模型名 / API Key / 接口格式（OpenAI 或 Anthropic）。
2. 保存前会**自动做连通性测试 + 一次 `hi` 对话测试**，失败会给出原因。
3. 配置好后即可在笔记/存档/Topic 页用 🤖 AI Chat、AI 总结。

> 没有真实 Key 想先联调前端？在开发机跑 `python _fake_llm.py`（监听 5199，返回假流式），然后在设置里填 Base URL=`http://127.0.0.1:5199/v1`、任意模型名即可。

---

## 7. 故障排查速查

| 现象 | 处理 |
|------|------|
| 双击没反应 | 先看是否已有一个实例在跑（单实例机制）；或在命令行 `.\noteharness.exe` 看报错 |
| 白屏 / 启动卡住 | 删除 `data/gui-mode.txt` 重新探测；或加 `-app-window` 用浏览器内核窗口 |
| 想换端口 | `-serve -port 8080` |
| WebView2 崩溃弹框 | 程序会自动降级到独立窗口并记住该模式；也可手动 `-app-window` |
| 调用 LLM 报错 | 检查设置里的 Base URL、模型名、Key、格式是否正确，以及本机能否访问该地址 |

---

## 8. 目录与模块速览（改代码参考）

| 文件 | 职责 |
|------|------|
| `main.go` | 入口、服务模式、单实例检测、端口分配、`//go:embed static` |
| `gui.go` | Windows/macOS 窗口策略 + WebView2 探测与自动降级（构建标签 `windows||darwin`） |
| `gui_other.go` | 其它平台（Linux）降级为服务模式（构建标签 `!windows && !darwin`） |
| `api.go` | 全部 REST 接口（state/notes/archive/tags/chats/export/config/llm） |
| `store.go` | JSON 持久化、笔记/存档/对话的增删改查、切片字段归一化防前端崩溃 |
| `llm.go` | LLM 代理转发（OpenAI / Anthropic），SSE 流式解析、token 估算 |
| `static/*` | 内嵌前端 SPA（vanilla JS + marked.js，离线可用） |

构建命令回顾：
```powershell
go build -tags production -ldflags "-H windowsgui -s -w" -o noteharness.exe .
```
