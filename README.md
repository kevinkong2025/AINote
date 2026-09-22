# NoteHarness — 提示词笔记库

单机版 AI 笔记工具：把你每天写的提示词、技术方案、踩坑记录按「笔记 → 存档 → Topic」三级归纳，
配合 AI 总结与多轮讨论，解决「周/年度总结不知道干了啥、整理简历遗漏项目、老问题找不到旧方案」三个痛点。

## 运行

- **Windows**：双击 `noteharness.exe`，自动弹出窗口，无需登录。
- **无桌面环境 / Linux**：`./noteharness -serve`，自动打开浏览器访问 `http://127.0.0.1:5177`（`-port 8080` 指定端口）。

### 启动策略（全自动）

1. **单实例**：已有一个实例在跑时，再双击只会把它的窗口再开一个，不会启动第二份程序。
   （多份进程各持一份内存数据，会互相覆盖 —— 这是「新建草稿没反应 / 草稿消失」的根因。）
2. **窗口模式**：首次启动会用一次性子进程探测 WebView2 能否**稳定渲染**（加载真实页面 + 观察 3 秒），
   结果记在 `data/gui-mode.txt`，之后直接按该模式启动（约 0.5 秒）。
   - `webview`：WebView2 原生窗口；
   - `app`：独立应用窗口（Edge/Chrome `--app`，无地址栏/标签页，独立任务栏图标），
     程序内部仅在 127.0.0.1 起本地服务供该窗口访问，界面与功能完全一致。
3. **崩溃接管**：原生窗口跑在子进程里。Wails 在 WebView2 浏览器进程崩溃时会弹框并 `os.Exit(-1)`，
   父进程检测到后自动切到独立应用窗口，并记住该模式，下次不再走 WebView2。
4. **干净退出**：关闭独立应用窗口后进程随之退出，不留后台僵尸、不占端口。

手动覆盖：`noteharness.exe -app-window`（强制独立窗口）/ `-webview`（强制原生窗口）。

数据保存在 exe 同级的 `data/store.json`，单文件、可直接备份（文件损坏会自动备份为 `*.corrupt-*` 并以空库启动）。
顶栏「⬇ 导出」可打包下载全部笔记为 zip（Markdown 文件，按 日期/存档日 分目录），方便迁移到其他环境。

## 功能地图

| 页面 | 能力 |
|------|------|
| 笔记 | 新建草稿（自动 draft 1/2/3…，创建即改名）、Markdown 编辑/预览、Ctrl+S 保存、Ctrl+Z 撤回、AI 总结（生成 `AI summary 年月日 原名`）、单篇/多篇存档、两篇 diff 并列比对（中间箭头左右替换差异块）、右键删除（就地确认）、🤖 调起 Agent 讨论 |
| 存档 | 年/月/日树（日期+数量圆点，自动展开最新）、标题搜索、只读查看/编辑保存、多选打 Tag（支持模糊搜索已有 Tag）、多选 AI Chat、两篇 diff、顶部展示 Tag |
| Topic | Tag 卡片墙（随机底色、最近使用日期）、点入后左侧竖排标签栏（顶部 ▼ 返回）、按时间降序文档列表、多选 AI Chat |
| 设置 | 多 LLM 配置（别名/Base URL/模型/APIKey/最大上下文/OpenAI 或 Anthropic 格式）；保存前先做「连通性测试 + hi 对话测试」，失败弹窗给出原因；AI 选项（快捷泡泡）增删改；导出 |
| AI Chat | 多对话窗口本地持久化、选中对话自动跳转左侧对应笔记、流式输出、上下文始终携带最初笔记（多篇时先 compact）、预设泡泡流式填入输入框、模型别名+名称点击切换、↑↓ token 统计、用户消息 hover 支持 复制/编辑/回退/删除 |

任何未捕获的异常都会在界面上提示（右下角 toast / 弹窗），不会「点了没反应」。

## 开发

```bash
# Windows GUI（必须带 -tags production，否则 Wails 会弹「缺少 build tags」提示）
go build -tags production -ldflags "-H windowsgui -s -w" -o noteharness.exe .

# Linux（服务模式；Wails 的 Linux 前端需要 cgo + webkit2gtk，由 gui_other.go 自动降级）
GOOS=linux GOARCH=amd64 go build -tags production -ldflags "-s -w" -o noteharness .
```

技术栈：Go 标准库 + Wails v2（原生窗口壳）+ 内嵌 HTML/JS（marked.js 已本地化，离线可用）。
LLM 请求由 Go 后端代理转发（OpenAI `/chat/completions`、Anthropic `/messages`，SSE 流式）。

源文件：`main.go`（入口/服务模式/单实例）、`gui.go`（窗口策略 + WebView2 探测与降级）、
`gui_other.go`（Linux 降级）、`api.go`（REST 接口）、`store.go`（JSON 持久化）、`llm.go`（LLM 代理）。
