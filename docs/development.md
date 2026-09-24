# 开发说明

## 目录结构

| 路径 | 内容 |
|---|---|
| `cmd/cline-pass-switcher` | 程序入口：打开数据目录、启动 HTTP 服务、处理退出信号。 |
| `internal/httpapi` | HTTP 路由、认证与限流、Chat 和 Responses 接口、流式转发、流共享与回放、请求历史记录。 |
| `internal/upstream` | 访问 Cline Pass 上游：账号选择与冷却、配额读取、会话粘性、渠道偏好、模型探测、超时控制。 |
| `internal/responses` | Responses 与 Chat Completions 之间的转换、上下文压缩、结构化输出校验。 |
| `internal/store` | 数据目录：配置与元数据快照、操作日志、目录锁。 |
| `internal/model` | 配置和数据结构、默认值、环境变量读取与规范化。 |
| `internal/sse` | 增量 SSE 解析器。 |
| `internal/apierr` | 上游错误的分类与统一的错误结构。 |
| `internal/jsonx`、`internal/strx` | JSON 与字符串的小工具。 |
| `internal/webassets` | 嵌入二进制的前端构建产物（`dist/`）。 |
| `web` | 控制台前端：React 19、TypeScript、Vite、Tailwind CSS。 |

## 环境要求

- Go 1.25 或更高版本。
- Node.js 与 npm，仅构建前端时需要。构建需要 Node 20.19+ 或 22.12+；运行前端单元测试需要 Node 22.22+ 或 24.15+。CI 和 Docker 镜像使用 Node 24。

## 构建

前端构建产物输出到 `internal/webassets/dist`，由 Go 的 `embed` 打进二进制，运行时不需要 Node.js。

```bash
cd web
npm ci
npm run build
cd ..
go build -trimpath -ldflags="-s -w" -o cline-pass-switcher ./cmd/cline-pass-switcher
```

`internal/webassets/dist` 需要随源码一起提交：CI 会重新构建前端，并检查结果与仓库里的内容一致。修改了 `web/` 下的代码，请记得重新构建并提交 `dist`。

## 前端开发

```bash
cd web
npm run dev
```

Vite 开发服务器把 `/api` 和 `/v1` 转发到 `http://127.0.0.1:3123`，所以需要先在本机启动网关。

## 测试

| 命令 | 目录 | 内容 |
|---|---|---|
| `go vet ./...` | 仓库根目录 | 静态检查 |
| `go test ./...` | 仓库根目录 | Go 单元测试与接口测试，全部使用本地模拟的上游，不访问真实服务 |
| `go test -race ./...` | 仓库根目录 | 竞态检测，需要启用 cgo；在 Windows 上需要额外安装 C 编译器 |
| `npm run lint` | `web` | oxlint，警告也视为失败 |
| `npm test` | `web` | Vitest 单元测试 |
| `npm run test:e2e` | `web` | Playwright 端到端测试：用临时数据目录和固定密钥启动真实的网关，在 Chromium 中操作控制台。首次运行前需要 `npx playwright install chromium`。 |

有两个默认跳过的长时间测试，只在设置了环境变量时运行，也不访问真实上游：

```bash
# 超过 120 秒的长流
CLINE_MANUAL_LONG_STREAM=1 go test ./internal/httpapi -run ManualStreamPastLegacy120sDeadline -v -timeout 180s

# 共享流压力测试，持续指定的秒数
CLINE_STRESS_SECONDS=60 go test -race ./internal/httpapi -run SharedStreamSoak -v -timeout 4m
```

`internal/httpapi/testdata` 里有一段真实的上游流式响应，用来验证流式转换在任意分片位置都能得到相同的结果；更新方法见该目录下的 README。

## CI

每次推送和拉取请求都会运行 `.github/workflows/ci.yml` 中的四个任务：

| 任务 | 内容 |
|---|---|
| `go` | 在 Ubuntu 和 Windows 上运行 `go vet` 和 `go test`；Ubuntu 上额外运行 `go test -race`。 |
| `frontend` | `npm run lint`、`npm test`、`npm run build`，然后检查 `internal/webassets/dist` 与提交的内容一致。 |
| `e2e` | Playwright 端到端测试。 |
| `container` | 构建 Docker 镜像，检查容器以 UID 10001 运行且 `/data` 可写。 |
