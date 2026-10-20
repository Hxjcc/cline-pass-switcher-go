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

发布构建可以注入版本号（启动日志和上游 `User-Agent` 会使用它）：

```bash
VERSION=1.0.0
go build -trimpath \
  -ldflags="-s -w -X github.com/Hxjcc/cline-pass-switcher-go/internal/buildinfo.Version=${VERSION}" \
  -o cline-pass-switcher ./cmd/cline-pass-switcher
```

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

另有默认跳过的真实压缩评测，使用合成对话，检查连续三轮压缩后的任务约束、工具结果和图片视觉信息。开启后会调用指定模型并产生费用；不会修改远程配置。示例（PowerShell，请替换测试地址、模型和临时密钥）：

```powershell
$env:CLINE_LIVE_COMPACTION = "1"
$env:CLINE_TEST_BASE_URL = "https://your-gateway.example/v1"
$env:CLINE_TEST_MODEL = "cline-pass/your-model"
$env:CLINE_TEST_API_KEY = "your-temporary-test-key"
go test ./internal/httpapi -run '^TestLiveCompaction' -v -count=1 -timeout 12m
Remove-Item Env:CLINE_LIVE_COMPACTION, Env:CLINE_TEST_BASE_URL, Env:CLINE_TEST_MODEL, Env:CLINE_TEST_API_KEY
```

密钥只由内存中的测试代理注入请求头，临时数据目录保存的是占位账号密钥。普通 `go test ./...` 会跳过这些评测。

长上下文评测单独开启，不随上面的普通真实评测运行。它按约 10 万、30 万、90 万输入 token 逐档测试，后续档位根据上游实际用量校准；最大档之后再追加两轮约 5 万 token 的新历史，检查七项早期约束、当前状态及新旧指令覆盖关系。输入包含不同的代码日志和工具调用。容量最终受实际转发链路限制，测试不把字符数当作 token 用量。

```powershell
# 保留上面的 CLINE_LIVE_COMPACTION 和三个 CLINE_TEST_* 接入变量。
$env:CLINE_LIVE_LONG_COMPACTION = "1"
$env:CLINE_TEST_TIMEOUT_SECONDS = "600"
$env:CLINE_TEST_REPORT_PATH = Join-Path $env:TEMP "cline-compaction-long-report.json"
go test ./internal/httpapi -run '^TestLiveLongCompaction' -v -count=1 -timeout 35m
Remove-Item Env:CLINE_LIVE_LONG_COMPACTION, Env:CLINE_TEST_TIMEOUT_SECONDS, Env:CLINE_TEST_REPORT_PATH
```

这项评测的真实调用量和费用高于普通评测。日志包含每次调用的实际输入 token、缓存命中、输出 token、耗时及上游报告费用；可选 JSON 报告只保存合成数据的统计与检查结果，不包含密钥。

`internal/httpapi/testdata` 里有一段真实的上游流式响应，用来验证流式转换在任意分片位置都能得到相同的结果；更新方法见该目录下的 README。

## CI

每次推送和拉取请求都会运行 `.github/workflows/ci.yml` 中的四个任务：

| 任务 | 内容 |
|---|---|
| `go` | 在 Ubuntu 和 Windows 上运行 `go vet` 和 `go test`；Ubuntu 上额外运行 `go test -race`。 |
| `frontend` | `npm run lint`、`npm test`、`npm run build`，然后检查 `internal/webassets/dist` 与提交的内容一致。 |
| `e2e` | Playwright 端到端测试。 |
| `container` | 构建 Docker 镜像，检查容器以 UID 10001 运行且 `/data` 可写。 |

## 依赖更新

Dependabot 每月检查一次 Go、npm 和 GitHub Actions 依赖；`minor` / `patch`
更新会按生态合并成一个 PR，`major` 更新保持独立 PR，便于人工判断。依赖 PR
仍需通过上面的 CI 后才能合并，不启用自动合并。

人工提交的前端改动如果改变了打包结果，合并前必须在 `web` 目录执行
`npm ci` 和 `npm run build`，并把 `internal/webassets/dist` 一起提交；
`frontend` 任务会检查内嵌产物是否与源码一致。

Dependabot 的 npm PR 只要求构建和测试通过，不要求分支里预先提交 dist；
合并到 `main` 后，`Sync embedded frontend` workflow 会自动重建并提交
`internal/webassets/dist`。这样依赖更新不会因为生成产物不同而反复失败。
Dependabot 安全更新在 GitHub 仓库设置中单独启用。
