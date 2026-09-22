# Cline Pass Switcher

本地 OpenAI 兼容代理，把 Cline、Codex 等客户端接到 [Cline Pass](https://cline.bot/)，并提供 Web 控制台管理账号、渠道和请求记录。

这是 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher) 的 Go 重写版：配置字段和 API 路径保持兼容，已有的 `config.json` / `metadata.json` 可以直接拿来用。

## 能做什么

- 在浏览器里管理账号、模型和渠道，查看请求轨迹
- 单账号或账号池轮询；账号 401 / 403 / 429 后自动冷却，并在同一条渠道内先换账号、再换渠道
- 账号池可显示套餐配额：5 小时 / 7 天 / 30 天的用量进度条与重置时间。任一窗口达到 100% 时，还有其他可用账号就会跳过该账号，直到这个窗口重置
- 探测上游渠道，按模型钉住、排除或按成本 / 首字 / 吞吐排序
- 失败后按顺序切换下一条渠道；流式输出在首包前仍可换渠道
- 兼容 OpenAI Chat Completions，以及 Codex 使用的 Responses API
- 前端已经打进单一可执行文件，运行时不需要 Node.js

## 快速开始

### Docker（推荐）

需要已安装 [Docker](https://docs.docker.com/get-docker/)。

Linux / macOS：

```bash
mkdir -p data
cp config.example.json data/config.json
docker compose up -d --build
```

Windows PowerShell：

```powershell
New-Item -ItemType Directory -Force data | Out-Null
Copy-Item config.example.json data\config.json
docker compose up -d --build
```

打开 <http://127.0.0.1:3123/>，在「账号管理」里填入 Cline Pass API Key 并保存。

配置和运行数据都在 `./data`。默认只监听本机 `127.0.0.1:3123`，不会暴露到局域网。

容器以非 root 用户运行：启动时 entrypoint 会先把 `data` 目录的属主改成运行用户（`PUID`/`PGID`，默认 `10001`），再降权执行，所以普通 `docker compose up -d --build` 即可，不需要在命令行前缀里传 PUID/PGID。想指定别的 uid（例如与宿主机用户一致）时，在同目录的 `.env` 里写 `PUID=1000` 和 `PGID=1000`；从旧版本升级且 `data` 属主混乱时，重启容器一次即可自动纠正。

### 容器默认开启的联网能力

`docker-compose.yml` 默认打开两个上游网关工具（**按次计费**，不需要就把对应行删掉或改成 `off`）：

| 环境变量 | 默认值 | 作用 |
|---|---|---|
| `WEB_SEARCH_UPSTREAM` | `exa` | 客户端声明 `web_search` 时改用网关的搜索工具，让模型能查最新信息 |
| `WEB_FETCH_UPSTREAM` | `browserbase_fetch` | 用户消息里出现链接时声明抓取工具，读取该页面的内容 |

两者都由上游网关（Cline → Vercel）执行，是否真的调用取决于模型：DeepSeek 会调用，GLM 目前不会。搜索不会返回结构化的 `url_citation`，来源以正文 URL 的形式给出。从源码运行时这两项默认关闭，需要在 `config.json` 里填 `webSearchUpstream` / `webFetchUpstream`，或设置同名环境变量。

公网 / 反向代理部署时，把 compose 里注释掉的 `PUBLIC_BASE_URL` 和 `PROXY_KEY` 打开并改成实际值——否则控制台的浏览器请求会因为来源校验返回 403（详见「从旧版本升级」）。

### 从源码运行

需要 Go 1.24+ 和 Node.js 22+。

```bash
cd web
npm ci
npm run build
cd ..
go run ./cmd/cline-pass-switcher
```

也可以打成单文件后再运行：

```bash
go build -o cline-pass-switcher ./cmd/cline-pass-switcher
./cline-pass-switcher
```

默认读取当前目录下的 `config.json`，监听 `127.0.0.1:3123`。用 Docker 时请走上面的 `./data` 目录。

## 从旧版本升级

- **访问控制变严**：未设置代理密钥（`proxyKey` / `PROXY_KEY`）时，只有连接来源确实是回环地址、且请求的 Host 也是回环地址（`127.0.0.1`、`localhost`，防 DNS 重绑定）才会被接受；只伪造 Host 头不再有效。用域名、局域网 IP、外部连接或反向代理访问会收到 403。容器端口映射把来源地址变成网桥网关，所以 Compose 里显式声明了 `TRUST_LOCAL_PORT_FORWARD=1`（见下面的环境变量）；HTTPS 反向代理部署必须设置 `PUBLIC_BASE_URL`，否则控制台请求会被判为跨源。
- **账号密钥默认脱敏**：`GET /api/accounts` 只返回 `keyPreview` 前后缀，需要完整密钥时显式请求 `GET /api/accounts?reveal=1`。控制台的「显示密钥」按钮就是这条路。
- **保存账号时留空表示保留原密钥**：控制台为每个账号分配了稳定的 `id`，提交时 `key` 为空且 `id` 匹配已有账号，则沿用已保存的密钥；新账号必须自带 `key`，否则会被忽略。
- **账号状态按 `id` 管理**：冷却、统计不再以账号名称为键，同名账号互不影响，改名也不会丢掉统计；旧版本按名称写入的统计会在启动时迁移到 `id`。更换密钥会立即清除旧密钥遗留的冷却。
- **旧顶层 `apiKey` 只迁移一次**：账号池为空时它会被并入账号池，随后从运行配置里丢弃，不再作为运行兜底——账号被停用或删除后请求会明确失败，而不是偷偷使用旧密钥。
- **数据目录新增运行文件**：`store.journal` 与 `.store.lock`。备份要包含整个目录，且同一目录只能运行一个实例（第二个实例会启动失败）。
- **容器以非 root 用户运行**：`PUID`、`PGID`（默认 `10001`）决定运行用户，启动时自动修正 `data` 属主，无需手动 `chown` 或命令行前缀，详见上一节。
- **工具历史不完整不再直接报错**：ChatGPT Desktop 等客户端会把「没有对应调用」的工具结果写进历史（跨任务委派、历史裁剪、宿主工具），旧版本会返回 400。现在这类内容会转成用户消息继续转发；需要恢复严格校验时设置 `STRICT_TOOL_HISTORY=true`。
- **请求历史落盘时机**：请求记录先写入日志缓冲，每 32 条或发生管理操作、正常退出时 `fsync`。进程崩溃不会丢记录（重启会重放日志），断电最多丢掉最近的少量记录；配置和模型改动仍然每次都同步落盘。

## 接入客户端

```text
Base URL: http://127.0.0.1:3123/v1
API Key:  控制台里设置的代理密钥；留空则不校验
Model:    cline-pass/glm-5.3-flash
```

Codex 使用 Responses 协议时，可在 `~/.codex/config.toml` 里加自定义 provider：

```toml
model_provider = "cline-pass"
model = "cline-pass/deepseek-v4.1-flash"
model_reasoning_effort = "high"

[model_providers.cline-pass]
name = "Cline Pass"
base_url = "http://127.0.0.1:3123/v1"
wire_api = "responses"
requires_openai_auth = false
env_key = "CLINE_PROXY_KEY"
```

`CLINE_PROXY_KEY` 填控制台里的代理密钥。代理密钥为空时，Codex 仍需要一个非空占位值。Responses 路径是 `POST /v1/responses`。

## 环境变量

| 变量 | 说明 |
|---|---|
| `CLINE_PASS_KEY` | Cline Pass API Key，启动时写入账号池 |
| `PROXY_KEY` | 下游代理和控制台 API 密钥 |
| `PUBLIC_BASE_URL` | 控制台对外访问地址，HTTPS 反向代理部署时需设置 |
| `PORT` | 监听端口，默认 `3123` |
| `BIND_HOST` | 监听地址，本地默认 `127.0.0.1`，容器内应设为 `0.0.0.0` |
| `TRUSTED_PROXIES` | 逗号分隔的 IP / CIDR 列表。只有来自这些地址的反向代理请求才会采用 `X-Forwarded-For` / `X-Real-IP` 声明的客户端地址（且客户端必须是回环地址才视为本机）。仅在没有代理密钥时生效 |
| `TRUST_LOCAL_PORT_FORWARD` | 设为 `true` / `1` 时，声明非回环来源只可能来自绑定在宿主机回环上的端口映射（如 `-p 127.0.0.1:3123:3123`）。Compose 默认打开；把端口改成对外暴露时必须删除并设置 `PROXY_KEY`。这层声明是唯一的判断依据，剩下的 Host 头校验由客户端控制，所以端口一旦对外暴露，伪造 `Host: 127.0.0.1` 的请求就会拿到完整管理权限（可读取账号密钥）。未设置代理密钥且打开该开关时，启动日志会打印对应警告 |
| `DATA_DIR` | 配置和元数据目录，容器内为 `/data` |
| `STRICT_TOOL_HISTORY` | 设为 `true` 时，工具历史不完整（客户端回放了没有对应调用的工具结果）直接报错；默认 `false`，这类结果会作为用户内容继续转发 |
| `WEB_SEARCH_UPSTREAM` | 把 `web_search` 声明映射成上游网关执行的搜索工具：`exa`（推荐）/ `tako` / `perplexity` / `browserbase_fetch`；留空或 `off` 表示不映射（默认） |
| `WEB_FETCH_UPSTREAM` | 用户消息里出现 http(s) 链接时，声明上游网关的抓取工具读取该页面：`browserbase_fetch`；留空或 `off` 表示关闭（默认） |
| `SHELL_COMPAT` | 转发给模型的工具 schema 里，凡是声明了 `shell` 参数的工具（`exec_command` 等）都限定为该值并设为必填，例如 `powershell`；留空或 `off` 表示不改（默认）。只对 Windows 客户端有意义——模型忘带 `shell` 时客户端会回退 `cmd.exe`，PowerShell 语法的命令就会报"不是内部或外部命令" |
| `SHELL_COMPAT_ENFORCE` | 设为 `true` 时，除了收窄 schema，还会在参数完整后把实际工具调用的 `shell` 字段改写成 `SHELL_COMPAT` 的值；默认关闭。只对声明了 `shell` 参数的工具生效，并要求 `SHELL_COMPAT` 已设置 |

Docker Compose 已经设置了 `DATA_DIR` 和 `BIND_HOST`。镜像里的 `PORT=3123` 会覆盖 `config.json` 中的端口，与端口映射保持一致。

挂在 nginx、Traefik 等反向代理后面时，保持本机绑定即可。流式输出需要关闭响应缓冲，并加大读取超时。

## 配额探测（未公开端点）

控制台的「查询配额」读取 Cline dashboard 自己使用的两个端点（官方文档没有列出，字段可能变化）：

| 端点 | 内容 |
|---|---|
| `GET /users/me/plan` | 套餐名、是否有效、订阅周期，以及 `entitlements.cline_pass.inferenceCapThreshold` 的三个用量上限 |
| `GET /users/me/plan/usage-limits` | `five_hour` / `weekly` / `monthly` 的已用百分比与重置时间 |

两者都用账号 key 鉴权、只读、不消耗推理额度。任一窗口的已用百分比达到 100% 时，只要账号池里还有其他可用账号，选号会跳过它，直到该窗口的 `resetsAt`；读不到重置时间则跳过 5 分钟后再查。只有一个可用账号、每个账号都已满，或这次探测失败时，仍使用原来的账号，转发不会因为查不到配额而停住。控制台查询和转发前的检查共用短缓存：未满或探测失败的结果保留约 1 分钟，已满的结果保留到重置时间，避免每个请求都打这两条端点。

单位：整数金额字段是 **1e-8 美元**（`1e8 = $1`）。实测同一次请求：网关返回 `cost = $0.000006`，官方台账记为 `costUsd = 600`；所以上限 `1_000_000_000 / 2_500_000_000 / 5_000_000_000` = **$10 / 5 小时、$25 / 7 天、$50 / 30 天**。

## 安全

- `config.json`、`metadata.json` 和 `data/` 里可能有明文密钥，不要提交到 Git。
- 未设置代理密钥时，控制台与本机 API 只接受真实来源为回环地址的连接；`Host` 只是额外的 DNS 重绑定校验，不能作为网络边界。`TRUSTED_PROXIES`、`TRUST_LOCAL_PORT_FORWARD` 是运维对网络边界的显式声明，不要在没有这层边界时打开：打开后判定只剩 `Host` 头，而它是客户端可控的。
- 使用域名、非回环 IP 或对其他机器开放前，务必设置代理密钥（`proxyKey` / `PROXY_KEY`）。该密钥也具有管理权限。
- 控制台接口默认不返回已保存的密钥（只有 `keyPreview`），需要时才通过 `GET /api/accounts?reveal=1` 显式读取，且响应带 `Cache-Control: no-store`。
- 控制台页面带 `Content-Security-Policy`（只允许自身资源，加上 `index.html` 里两段内联启动脚本的 SHA-256 白名单，启动时按文件内容计算）、`X-Frame-Options: DENY` 和 `Referrer-Policy: no-referrer`，避免页面被嵌套或注入脚本后读走密钥。
- 控制台输入的代理密钥默认只写入 `sessionStorage`（关掉标签页即失效），需要在登录框勾选「在这台设备上记住密钥」才会写入 `localStorage`。
- 代理密钥校验有失败限速：同一来源连续失败 5 次后开始冷却，首次 30 秒、重复触发成倍延长、上限 15 分钟，冷却期间该来源的请求直接返回 429（即使密钥正确）；成功认证会清零计数。计数只在内存中，重启进程即清除。注意 Docker 端口映射会把所有客户端显示成同一个网桥地址，因此这些客户端共享同一个冷却计数。
- 浏览器需同源访问控制台和接口；HTTPS 反向代理部署时设置 `PUBLIC_BASE_URL`。
- 控制台里的探测、测试、校验会向真实上游发小额请求。

## 数据备份

每个数据目录只供一个服务实例使用。备份、迁移或手动修改配置前，请先正常停止服务。备份整个数据目录，包括 `store.journal`，不要单独删除其中的运行文件。

## 致谢

感谢原作者 [@munmunjaklin458-afk](https://github.com/munmunjaklin458-afk) 开源 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher)。

## License

[MIT](LICENSE)
