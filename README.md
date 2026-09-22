# Cline Pass Switcher (Go)

<p align="center">
  <strong>专为 Cline、Codex CLI 及 OpenAI 兼容生态打造的高性能本地网关与智能多账号调度代理</strong>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go" alt="Go Version" />
  <img src="https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker" alt="Docker Ready" />
  <img src="https://img.shields.io/badge/Node.js-22+-(Build%20Only)-339933?style=flat-square&logo=node.js" alt="Node Version" />
  <img src="https://img.shields.io/badge/License-MIT-green?style=flat-square" alt="License" />
</p>

---

## 📖 项目简介

**Cline Pass Switcher** 是一个运行在本地的高性能中继网关。它将来自 **Cline**（VS Code 插件）、**Codex CLI**（ChatGPT Desktop / CLI）等客户端的请求，无缝转换为对 [Cline Pass](https://cline.bot/) 上游 API 的标准调用。

服务端采用 **Go 语言**重构开发，前端控制台（React + Tailwind CSS）在构建阶段直接静态打包并内嵌至二进制文件（`embed.FS`）中，**运行时零 Node.js 依赖**，具备毫秒级冷启动、极低内存占用以及工业级的稳定性。

```
客户端层
  Cline / Codex CLI / Continue
        │
        │ HTTP (OpenAI Chat / Responses 协议)
        ▼
Cline Pass Switcher (Go)
  ├─ 协议转换与适配
  │  ├─ Chat Completions
  │  ├─ Responses 桥
  │  └─ 结构化校验
  ├─ 智能账号池与健康调度
  │  ├─ 轮询 / 固定 + 备用
  │  ├─ 401/403/429 动态冷却
  │  └─ 5h / 7d / 30d 配额自动避让
  ├─ 渠道调度与探测
  │  ├─ 渠道钉选 / 排除
  │  └─ 成本 / 时延排序
  ├─ 会话粘性与流式回放
  │  ├─ 30min Prompt Cache 锁定
  │  └─ 共享流分发 / 内存磁盘溢出
  ├─ WAL 预写日志 (`store.journal`) + JSON 快照持久化
  └─ 内嵌 Web 管理控制台（默认端口 3123）
        │
        │ HTTPS (Cline Pass 官方网关)
        ▼
Cline Pass 上游
  DeepSeek / GLM / Kimi / Qwen…
```

---

## ✨ 核心特性

- 🚀 **双协议原生支持**
  - **OpenAI Chat Completions 协议**：标准兼容各大主流客户端与开发框架（`/v1/chat/completions`）。
  - **OpenAI Responses 协议桥接**：完整实现 `/v1/responses` 及 `/v1/responses/compact` 接口，无服务端状态持久化要求，完美支持 Codex CLI、ChatGPT Desktop 的多轮交互与上下文自动压缩。
- 👥 **企业级多账号池调度**
  - 支持 **主备模式（固定账号）** 与 **均衡轮询（Round-Robin）**。
  - **自适应健康探测与冷却**：遭遇 `401/403` 自动冷却 10 分钟；遭遇 `429` 限流自动冷却 2 分钟，秒级无缝漂移到下一个可用账号。
  - **5h / 7d / 30d 配额深度监控**：自动探查套餐窗口利用率，任一周期耗尽（100%）时自动避让至重置时间（`resetsAt`），零额外扣费，探查失败不阻塞正常推理。
- 🧠 **会话粘性（Session Stickiness）与 Prompt Cache**
  - 自动识别客户端的 `prompt_cache_key`（如 Codex 会话），将同一会话锁定在上次成功的账号与渠道上达 30 分钟，最大化利用服务端 KV 缓存降低首字延迟与调用资费。
- 🎯 **精细化渠道控制与钉选**
  - 针对支持渠道钉选的模型（如 GLM 部分渠道），支持自定义优选渠道、排除不可用渠道，并按 **成本、首字延迟（TTFT）、吞吐量（TPS）** 自动测速排序。
  - 针对只支持自动路由的模型（如当前 DeepSeek），网关探测后自动退化为安全自动路由，防止无效重试。
- 🛠️ **桌面客户端与 Agent 兼容补丁**
  - **工具历史归一化（Orphan Tool Normalization）**：桌面客户端产生的未配对孤儿工具输出（如委派子任务 `<codex_delegation>`、`create_thread`）自动转写为安全用户消息，防止上游报错。
  - **联网搜索与抓取映射**：将客户端 `web_search` 无缝映射到网关服务端工具（`exa` / `tako` / `perplexity`）；用户消息附带 URL 时自动触发 `browserbase_fetch` 页面抓取。
  - **Windows Shell 兼容保障**：`SHELL_COMPAT=powershell` 强制收拢执行器声明，杜绝客户端意外回退至 `cmd.exe`。
  - **Token 膨胀折算**：智能折叠网关内部多轮搜索的 token 累计值，防止 Codex 发生误判而提前强制压缩上下文。
  - **本地严格 JSON Schema 校验**：集成 `draft 2020-12` 校验器，在上游输出不符合 strict schema 时即刻拦截报错。
- ⚡ **极致性能与流式回放**
  - 极低内存占用（微基准测试单流内存开销降低 ~98%）。
  - **共享流分发（Stream Share Hub）**：支持多订阅者复用上游同一 SSE 数据流；采用分页内存块（32 KiB 块）与自动临时文件溢出机制（单流内存 1 MiB、进程合计 32 MiB，超过即整条流写入临时文件；回放总量单流上限 128 MiB、进程合计 1 GiB），确保长流高并发下绝无内存泄漏。
  - 客户端每 10 秒 SSE 心跳保活，防止各类云厂商反向代理超时断连。
- 📊 **现代化全功能可视化控制台**
  - 账号管理、模型订阅、渠道钉选、在线测试台、实时请求调用链追踪（含耗时、TTFT、账单 token 及费用详情）。
- 🛡️ **高安全与持久化**
  - **WAL 预写日志（`store.journal`）**：批量 fsync 与序列号校验。进程崩溃不丢记录（重启会重放日志）；意外断电最多丢失最近的少量记录（请求记录每 32 条 fsync 一次，管理操作与正常退出也会落盘）。
  - 严格的主机访问防护、CSP 策略、防密码暴力破解限流保护。

---

## 🚀 快速开始

### 方式一：Docker Compose（推荐，开箱即用）

1. **准备配置文件与数据目录**：

   **Linux / macOS**：
   ```bash
   mkdir -p data
   cp config.example.json data/config.json
   docker compose up -d --build
   ```

   **Windows PowerShell**：
   ```powershell
   New-Item -ItemType Directory -Force data | Out-Null
   Copy-Item config.example.json data\config.json
   docker compose up -d --build
   ```

2. **访问控制台**：
   打开浏览器访问：**<http://127.0.0.1:3123/>**  
   在 **「账号池」** 面板填入你的 **Cline Pass API Key** 并保存，即可开始使用。

> **权限说明**：容器默认以非 root 用户（UID/GID `10001`）运行，容器入口脚本会自动修正挂载卷所有权，无需手动 `chown`。若需指定用户，可在同级 `.env` 文件中设置 `PUID=1000` 与 `PGID=1000`。

---

### 方式二：本地源码编译运行

#### 环境要求
- **Go**: 1.24 或更高版本
- **Node.js**: 22+ 及 npm（仅构建前端资源时需要）

#### 构建步骤
```bash
# 1. 构建前端静态资源（产物内嵌到 Go package 中）
cd web
npm ci
npm run build
cd ..

# 2. 编译 Go 二进制文件
go build -trimpath -ldflags="-s -w" -o cline-pass-switcher ./cmd/cline-pass-switcher

# 3. 运行服务
./cline-pass-switcher
```

> 源码直接运行时默认读取当前目录下的 `config.json`（若不存在则自动初始化），并监听 `127.0.0.1:3123`。

---

## 🔌 客户端接入指南

### 1. Cline (VS Code 扩展)

在 VS Code 的 Cline 插件设置中配置自定义 Provider：

- **API Provider**：`OpenAI Compatible`
- **Base URL**：`http://127.0.0.1:3123/v1`
- **API Key**：若控制台设置了代理密钥，则填入该密钥；若未设置则可任意填写占位符（如 `sk-local`）
- **Model ID**：输入已订阅的模型名称，例如：
  - `cline-pass/deepseek-v4.1-flash`
  - `cline-pass/glm-5.3-flash`
  - `cline-pass/qwen3.8-max`

---

### 2. Codex CLI / ChatGPT Desktop

编辑 Codex 配置文件（位于 `~/.codex/config.toml`）：

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

在系统环境变量中设置：
```bash
export CLINE_PROXY_KEY="your-proxy-key" # 若未开启代理密钥，可填任意非空字符串
```

---

### 3. 通用客户端（Continue / Roo Code / Python SDK / Curl）

**标准路由路径一览**：
| 协议类型 | 路由端点 | 说明 |
|---|---|---|
| **Chat Completions** | `POST /v1/chat/completions` (或 `/chat/completions`) | 兼容标准 OpenAI 客户端 |
| **Responses** | `POST /v1/responses` (或 `/responses`) | Codex / ChatGPT 协议 |
| **Responses Compact** | `POST /v1/responses/compact` | Codex 上下文压缩端点 |
| **Models 列表** | `GET /v1/models` (或 `/models`) | 获取已订阅/全量模型清单 |
| **健康检查** | `GET /healthz` | 服务健康探针 |

**Curl 快速验证**：
```bash
curl -X POST http://127.0.0.1:3123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_PROXY_KEY" \
  -d '{
    "model": "cline-pass/deepseek-v4.1-flash",
    "messages": [{"role": "user", "content": "你好"}],
    "stream": false
  }'
```

**响应头诊断信息（Response Headers）**：
网关在每次响应头中注入了丰富的链路追踪元数据：
- `X-Cline-Account`：最终命中调用的账号名称。
- `X-Cline-Target-Upstream`：计划尝试的渠道（多个以 `>` 连接，自动路由为 `auto`）。
- `X-Cline-Actual-Upstream`：上游网关实际落到的渠道 slug。
- `X-Cline-Canonical-Model`：网关背后的底层规范模型标识。
- `X-Cline-Attempts`：本次请求向真实上游发起调用的总次数。
- `X-Cline-Reasoning-Effort`：实际转发给上游的思考强度参数。

---

## 🖥️ Web 管理控制台

访问 `http://127.0.0.1:3123/` 即可进入全功能可视化面板：

| 面板 | 功能说明 |
|---|---|
| 📋 **模型与上游** | 查看已订阅模型列表。对支持钉选的模型可配置优先渠道与排除渠道；支持按 **成本**、**首字延迟（TTFT）** 或 **吞吐速率（TPS）** 一键测速排序并探测渠道健康度。 |
| 👥 **账号池** | 添加、停用、测试 Cline Pass 密钥；在 **固定模式** 和 **轮询模式** 间切换；实时监控 5 小时、7 天、30 天用量百分比及重置倒计时。 |
| 🛡️ **访问与安全** | 设置统一代理访问密钥（`proxyKey`）、对外公开基准 URL（`publicBaseUrl`）以及模型目录公开开关。 |
| 🧪 **测试台** | 在线向指定模型发送真实测试请求，实时查看流式输出、命中的账号与渠道、推理耗时及完整调用链诊断。 |
| 📜 **请求历史** | 实时查看最近 500 条请求的完整流水，展示耗时、TTFT、Prompt/Completion/Reasoning Token 明细、费用及错误原因；支持关键字搜索与只看失败。 |
| 🌐 **完整目录** | 实时拉取 Cline Pass 官方最新模型目录，支持一键加入订阅或移除已有模型。 |

---

## ⚙️ 核心调度机制深入解析

### 1. 账号状态机与自适应冷却
- **可用性条件**：账号必须同时满足已启用且包含非空 API Key。
- **调度策略**：
  - **固定账号**：优先使用标为活跃的账号。当其触发冷却或配额用满被暂时跳过时，按列表顺序尝试后续第一个可用账号（不轮循剩余账号）。
  - **轮询模式**：在当前所有可用的健康账号之间以 Round-Robin 算法均匀分发。
- **自动冷却策略（内存记录，重启复位）**：
  - **401 / 403 凭据失效**：账号自动进入 **10 分钟** 冷却期，请求在同一渠道上改用下一个健康账号重试。
  - **429 请求超限**：账号进入 **2 分钟** 冷却期。
  - 账号下一次请求成功或用户更新密钥后，旧冷却记录自动清除。
- **配额过载防护（5h / 7d / 30d 窗口）**：
  - 用量按需读取，没有后台定时轮询：你在控制台点「查询配额」，或转发前选号时才会请求 Cline 官方用量监控接口（`GET /users/me/plan`、`GET /users/me/plan/usage-limits`，只读且不计入模型 token 消耗）。同一账号 60 秒内最多查一次，已满的账号保留到 `resetsAt` 不再重复查询；
  - 任一周期达到 100% 时，该账号被标记为耗尽，避让至该周期的 `resetsAt` 重置时间；
  - 若所有账号皆满额，网关不会拦截请求，仍会尝试发出以保障最大可用性。

### 2. 会话粘性（Session Stickiness）
Codex 等桌面客户端每次交互都会附带固定的会话标识 `prompt_cache_key`。网关捕获该 key 后：
- 在 30 分钟生命周期内，后续对话持续固定在同一账号及相同渠道上；
- 每次收到新请求自动续期 30 分钟；
- 仅当该账号触发限流、配额耗尽或被禁用时才释放粘性，重新落到健康账号后再度粘合。

### 3. 上游渠道重试与回退预算
为防止在上游故障时请求无限放大，网关内置了严格的尝试预算：
- 单个渠道单次最多尝试 4 个账号；
- 一次客户端请求向真实上游最多重试 16 次（`maxChainAttempts = 16`）；
- 若请求为流式且已开始向客户端返回内容（首包已交付），网关将锁定连接，不再执行破坏性的中途切换。

### 4. Responses 兼容桥与工具历史归一化
ChatGPT Desktop / Codex 客户端常出现孤儿工具调用记录（例如由后台任务委派引发的没有前置 `call` 的工具结果）。Chat Completions 上游通常会直接返回协议错误。
本代理内置智能历史归一化状态机：
- 识别 `create_thread`、`codex_app` 及 `<codex_delegation>` 委派前缀，安全解包转换为标准用户上下文；
- 其余孤儿结果自动加上 `[tool result without a recorded call <name>]` 前缀转为安全用户消息，确保上下文对话流严格保持 `assistant -> tool... -> user` 规范。若需要旧版严格校验，可设置 `strictToolHistory: true`。

---

## 🛠️ 配置参数与环境变量

可以通过修改 `config.json` 或设置系统环境变量来进行配置。环境变量优先级高于配置文件。

| 环境变量 | 对应 config.json | 默认值 | 作用与详细说明 |
|---|---|---|---|
| `PORT` | `port` | `3123` | 服务监听端口。 |
| `BIND_HOST` | - | `127.0.0.1` | 监听地址（源码运行默认 `127.0.0.1`，容器中设为 `0.0.0.0`）。 |
| `DATA_DIR` | - | `.` (容器为 `/data`) | 运行数据、配置和日志的存储目录。 |
| `PROXY_KEY` | `proxyKey` | 留空 | **代理密钥**。设置后，API 调用与 Web 控制台均需提供此密钥（`Bearer <key>` 或 `X-Admin-Key`）。 |
| `PUBLIC_BASE_URL` | `publicBaseUrl` | 留空 | 服务对外访问的基准 URL（如放在反代后设为 `https://api.example.com`）。 |
| `CLINE_PASS_KEY` | - | 留空 | 启动时默认注入账号池的初始 Cline Pass API Key。 |
| `WEB_SEARCH_UPSTREAM` | `webSearchUpstream` | Compose: `exa` / 源码: 留空 | 客户端声明 `web_search` 时映射的服务端工具：`exa` / `tako` / `perplexity`；设为 `off` 或留空表示关闭。 |
| `WEB_FETCH_UPSTREAM` | `webFetchUpstream` | Compose: `browserbase_fetch` / 源码: 留空 | 用户消息中出现 HTTP(S) 链接时自动声明的网页抓取工具：`browserbase_fetch`；留空或 `off` 表示关闭。 |
| `SHELL_COMPAT` | `shellCompat` | 留空 | 客户端工具兼容模式。在 Windows 客户端下推荐设为 `powershell`，强制模型在工具调用中声明 shell 参数。 |
| `SHELL_COMPAT_ENFORCE`| `shellCompatEnforce`| `false` | 是否强制把模型输出中的实际 `shell` 参数改写为 `SHELL_COMPAT`。 |
| `STRICT_TOOL_HISTORY` | `strictToolHistory` | `false` | 是否开启严格工具历史校验。开启后，未配对的孤儿工具结果将直接报错拒绝。 |
| `TRUSTED_PROXIES` | `trustedProxies` | `[]` | 信任的反向代理 IP 或 CIDR 列表（仅在未设置 `PROXY_KEY` 时生效）。 |
| `TRUST_LOCAL_PORT_FORWARD`| `trustLocalPortForward` | Compose: `1` | 信任本地端口映射（容器内将宿主机回环端口视作本机安全请求）。暴露公网时必须清除此项并配置 `PROXY_KEY`。 |

---

## 🌐 生产部署与反向代理

当挂载在 Nginx、Traefik、Caddy 等反向代理后面时，请确保开启流式长连接并关闭响应缓冲。

### Nginx 配置示例

```nginx
server {
    listen 443 ssl http2;
    server_name cline.example.com;

    ssl_certificate     /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://127.0.0.1:3123;
        proxy_http_version 1.1;

        # 核心设置：支持长思考模型的长连接与 SSE 流式推送
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

> **安全提示**：对外开放网络访问时，**务必在控制台或环境变量中设置强密码 `PROXY_KEY`**！未设置密钥且暴露端口将导致控制台管理权限完全失窃。

---

## 💾 数据存储与持久化

数据目录（`./data`）中核心包含以下文件：

| 文件 | 描述与维护说明 |
|---|---|
| `config.json` | 核心配置快照：包含账号列表、密钥、模型偏好与路由钉选设置。 |
| `metadata.json` | 运行时快照：包含渠道测速结果、官方模型目录缓存、最近 500 条请求历史。 |
| `store.journal` | WAL（Write-Ahead Log）操作日志：每次配置修改与请求记录均先追加至该日志并批量 fsync，定期合并入上述 JSON 快照中。 |

> **数据备份**：备份或迁移时，先停止容器或服务进程，直接复制整个 `./data` 目录即可。

---

## ❓ 常见问题 (FAQ)

<details>
<summary><strong>Q: 启动后提示 <code>尚未配置上游 API Key</code>？</strong></summary>
正常现象。服务已成功启动，只需打开浏览器访问 <code>http://127.0.0.1:3123/</code>，在「账号池」中添加你的 Cline Pass 密钥并保存即可。
</details>

<details>
<summary><strong>Q: 为什么 DeepSeek 模型无法钉选渠道？</strong></summary>
当前 Cline Pass 上游网关对 DeepSeek 系列实行全自动动态网关路由，显式指定渠道会被网关直接拒绝。控制台探测到此特性后会自动锁定该模型为自动路由模式，并避免做无谓的渠道遍历尝试。
</details>

<details>
<summary><strong>Q: 遇到 429 限流时，代理会怎么处理？</strong></summary>
如果账号池中有多个可用账号，网关会将遭遇 429 的账号放入 2 分钟冷却池，并在当前请求中自动切换至下一个可用账号继续尝试，对客户端完全透明。
</details>

<details>
<summary><strong>Q: 为什么请求历史里的 Token 数和客户端统计的不同？</strong></summary>
网关在执行内置搜索或多轮工具调用时，上游账单会将各个内部轮次的 Prompt 累加统计（控制台历史展示的是上游真实收费明细）；但为了防止 Codex 误以为上下文暴增而提前触发本地截断压缩，网关发回给客户端的 <code>usage</code> 经过了轮次归一化折算。
</details>

---

## 🤝 致谢与开源协议

- 感谢原作者 [@munmunjaklin458-afk](https://github.com/munmunjaklin458-afk) 开源 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher)。
- 本项目遵循 [MIT License](LICENSE) 开源协议。
