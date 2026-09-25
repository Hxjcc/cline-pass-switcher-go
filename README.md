# Cline Pass Switcher

把 [Cline Pass](https://cline.bot/) 订阅变成 OpenAI 兼容接口的网关，供 Cline、Codex CLI 以及其他 OpenAI 兼容客户端使用。用 Go 编写，控制台前端内嵌在二进制里，运行时只需要一个可执行文件和一个数据目录。

```
Cline / Codex / OpenAI SDK
        │  Chat Completions 或 Responses
        ▼
Cline Pass Switcher ── 账号池、渠道选择、协议转换、请求记录
        │  Chat Completions
        ▼
Cline Pass 上游（api.cline.bot）
```

## 功能

- **两种协议。** `/v1/chat/completions` 基本原样转发；`/v1/responses` 在网关内转换成 Chat Completions，支持工具调用、思考内容、Codex 远端上下文压缩和结构化输出校验。
- **账号池。** 可以添加多个 Cline Pass 账号，使用单账号或轮询模式。上游返回 401、403、429 时自动换账号重试；套餐用量已满的账号自动跳过，直到重置。
- **渠道控制。** 对支持的模型，可以指定优先渠道、排除渠道，或让网关按成本、首字延迟、吞吐选择渠道。
- **会话粘性。** 同一会话的后续请求优先使用上次成功的账号和渠道，便于命中上游的提示缓存。
- **分发密钥。** 可以签发多个客户端密钥分给他人使用，每个密钥可以绑定账号、设置美元额度上限。
- **控制台与请求历史。** 在浏览器里管理账号、模型、密钥和访问设置，查看最近 500 条请求的耗时、token、费用、实际渠道和错误原因。

## 快速开始

### Docker Compose（推荐）

```bash
git clone https://github.com/Hxjcc/cline-pass-switcher-go.git
cd cline-pass-switcher-go
docker compose up -d --build
```

浏览器打开 <http://127.0.0.1:3123/>，在「账号池」页添加 Cline Pass API Key 并保存，就可以开始使用。

- 端口只绑定在宿主机的 `127.0.0.1:3123`，数据保存在 `./data` 目录。
- 容器以 UID/GID `10001` 运行，启动时会自动修正 `./data` 的属主。需要其他用户时，在 `.env` 中设置 `PUID` 和 `PGID`。
- 本地配置写在同目录的 `.env` 文件里，不要修改 `docker-compose.yml`。可用的变量见[配置参考](docs/configuration.md#docker-compose-的默认值)。
- Compose 默认开启了联网搜索（`exa`）和网页抓取（`browserbase_fetch`），它们由上游按次计费，只在模型实际调用时产生费用。不需要时在 `.env` 中写 `WEB_SEARCH_UPSTREAM=off` 和 `WEB_FETCH_UPSTREAM=off`。

### 源码编译运行

需要 Go 1.25 以上；构建前端需要 Node.js 22.12 以上及 npm。

```bash
cd web
npm ci
npm run build
cd ..
go build -trimpath -ldflags="-s -w" -o cline-pass-switcher ./cmd/cline-pass-switcher
./cline-pass-switcher
```

默认监听 `127.0.0.1:3123`，数据保存在当前目录。可以用环境变量 `DATA_DIR` 指定数据目录，`PORT` 和 `BIND_HOST` 修改监听端口和地址。

## 接入客户端

| 项 | 值 |
|---|---|
| Base URL | `http://127.0.0.1:3123/v1`；对外部署时为 `PUBLIC_BASE_URL` 加 `/v1` |
| API Key | 代理主密钥 `PROXY_KEY` 或签发的代理密钥。都没有设置时只能从本机访问，可以填任意非空字符串 |
| 模型 | `cline-pass/` 开头的模型 ID，例如 `cline-pass/deepseek-v4.1-flash`。完整列表见控制台或 `GET /v1/models` |

### Cline

在 Cline 设置中选择 API Provider 为 **OpenAI Compatible**，填入上表中的 Base URL、API Key 和模型 ID。

### Codex CLI / Codex 桌面版

编辑 `~/.codex/config.toml`：

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

然后在环境变量 `CLINE_PROXY_KEY` 中设置密钥：

```bash
export CLINE_PROXY_KEY="你的代理密钥"
```

Codex 只对 OpenAI 和 Azure 形态的 provider 使用远端上下文压缩，其他名称的 provider 会退回客户端本地摘要。想让 Codex 的 `/compact` 和自动压缩交给网关处理（摘要之外还会原样保留最近几轮对话），把上面的 `name` 改成 `"azure"`，详见 [Responses 协议兼容 · 上下文压缩](docs/protocol-compatibility.md#上下文压缩)。

### 其他客户端

任何支持 OpenAI Chat Completions 的客户端或 SDK 都可以直接使用：

```bash
curl http://127.0.0.1:3123/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer 你的代理密钥" \
  -d '{"model": "cline-pass/deepseek-v4.1-flash", "messages": [{"role": "user", "content": "你好"}]}'
```

响应头 `X-Cline-Account`、`X-Cline-Actual-Upstream`、`X-Cline-Attempts` 等给出实际使用的账号、渠道和上游调用次数，见[账号与渠道调度 · 响应头](docs/routing.md#响应头)。

## 控制台

打开 `http://127.0.0.1:3123/`。设置了管理密钥时需要先登录；勾选「在这台设备上记住密钥」会保存在浏览器本地，否则关闭页面后失效。

| 页面 | 用途 |
|---|---|
| 模型与上游 | 订阅模型列表，也就是客户端从 `/v1/models` 看到的内容。可以拉取官方模型、批量探测、移除模型；展开一个模型可以设置钉住模式、优先渠道和排除渠道。 |
| 账号池 | 添加、启用、停用、删除账号，选择单账号或账号池轮询模式，测试账号，查看 5 小时、7 天、30 天的套餐用量。 |
| 代理密钥 | 签发客户端密钥，设置名称、绑定账号、额度上限和备注，查看每个密钥的累计消费并清零。 |
| 访问与安全 | 设置管理密钥、代理主密钥和公网访问地址；查看运行参数的当前生效值及来源；复制客户端接入地址。 |
| 测试台 | 向指定模型、指定渠道发送一条测试请求，查看实际命中的渠道、账号、耗时和尝试序列。 |
| 请求历史 | 最近 500 条请求，可按关键字筛选、只看失败、自动刷新或清空。 |

探测渠道、校验渠道、测试请求和测试账号都会向上游发出真实请求，会产生少量费用，见[账号与渠道调度 · 探测和校验会产生费用](docs/routing.md#探测和校验会产生费用)。

## 对外部署

默认配置只允许本机访问。要让其他机器通过域名使用，请逐项确认：

1. **设置密钥。** 在 `.env` 中设置 `ADMIN_KEY`（控制台）和 `PROXY_KEY`（客户端），或者只签发代理密钥给客户端。没有密钥的接口只接受本机访问。
2. **设置 `PUBLIC_BASE_URL`**，例如 `https://cline.example.com`。放在 HTTPS 反向代理后面时不设置它，控制台的保存操作会被拒绝。
3. **配置反向代理。** 关闭响应缓冲，读取超时不少于 600 秒，转发 `X-Real-IP` 和 `X-Forwarded-For`。
4. **设置 `TRUSTED_PROXIES`** 为反代连到网关时使用的地址。Docker 部署时通常是容器网络的网关地址，例如 `172.18.0.1`。不设置时，一个客户端反复用错密钥会让同一反代后面的所有人一起被暂时拒绝。
5. **保持 Compose 的端口绑定在 `127.0.0.1`**，只让反向代理访问网关。

Nginx 配置示例、`TRUSTED_PROXIES` 的查法和各种部署方式的区别，见[运维说明 · 反向代理](docs/operations.md#反向代理)。

## 分发代理密钥

在「代理密钥」页签发密钥，每个密钥可以单独设置：

- **绑定账号**：只使用这个账号，出错时不会改用其他账号的额度。
- **额度上限（美元）**：按上游报告的费用累计，达到上限后拒绝请求（HTTP 429）。有请求正在进行时，网关会按历史平均费用为它们预留额度，防止并发请求一起超出上限。

额度统计只计入上游报告的费用。客户端中途取消的流式请求拿不到最终费用，按 0 计，但上游仍会扣费，所以统计值可能比实际偏低。

分发密钥时建议同时设置 `ADMIN_KEY`，这样客户端密钥无法打开控制台，也就看不到账号池里的 API Key。详见 [HTTP 接口 · 代理密钥与额度](docs/api.md#代理密钥与额度)。

## 升级与备份

```bash
git pull
docker compose up -d --build
```

备份时先停止服务，再复制整个 `data` 目录。数据目录里有账号的 API Key，请注意保管。详见[运维说明](docs/operations.md)。

## 常见问题

**启动日志提示「尚未配置上游 API Key」。**
服务已经正常启动，打开控制台在「账号池」页添加账号并保存即可。

**在 `.env` 里把 `WEB_SEARCH_UPSTREAM` 设为空，联网搜索还是开着。**
控制台每次保存都会把当前生效的配置写进 `data/config.json`，之前的 `exa` 已经留在文件里，空的环境变量不会覆盖它。请写成 `WEB_SEARCH_UPSTREAM=off`。其他开关同理，见[配置参考](docs/configuration.md#配置从哪里来)。

**改了 `.env` 没有生效。**
环境变量只在启动时读取，改完要执行 `docker compose up -d`。Compose 只会把特定的几个变量传进容器，`PORT`、`CLINE_PASS_KEY`、`STRICT_TOOL_HISTORY` 写在 `.env` 里无效。可以在「访问与安全 → 运行参数」中确认当前生效值和来源。

**控制台里保存设置时提示 403。**
网关放在 HTTPS 反向代理后面，但没有设置 `PUBLIC_BASE_URL`。

**有些模型不能钉住渠道。**
这类模型在上游由网关自动选择渠道，渠道参数会被忽略（例如 DeepSeek 系列），或者只有一个渠道。探测后控制台会标出原因并禁用相关设置，请求仍然正常发送。

**请求历史里的 token 数和客户端显示的不一样。**
网关在一次请求内部执行多轮搜索或工具调用时，上游会把每一轮的输入 token 累加。请求历史记录上游的原始用量和费用；返回给 Codex 的用量按轮次折算过，避免它误以为上下文暴涨而提前压缩。

**请求了订阅列表里没有的模型。**
网关照样转发。如果是 `cline-pass/` 开头的模型并且请求成功，它会被自动加入订阅。

**用错几次密钥后，正确的密钥也被拒绝。**
同一来源 1 分钟内用错 5 次会被暂时拒绝，从 30 秒开始，每次翻倍，最长 15 分钟，期间正确的密钥也会被拒绝。等待响应头 `Retry-After` 给出的时间，或者重启服务清空计数。

## 文档

| 文档 | 内容 |
|---|---|
| [配置参考](docs/configuration.md) | 全部配置项、默认值、环境变量与 `config.json` 的优先级、Compose 的默认值 |
| [账号与渠道调度](docs/routing.md) | 账号选择、冷却、配额、会话粘性、渠道钉住、重试与超时、响应头 |
| [HTTP 接口](docs/api.md) | 模型接口、管理接口、密钥与额度、认证失败限流、错误格式 |
| [Responses 协议兼容](docs/protocol-compatibility.md) | 支持与不支持的功能、工具处理、联网搜索、结构化输出、上下文压缩 |
| [运维说明](docs/operations.md) | 数据目录、访问控制、反向代理、流共享的资源上限、启动与退出、升级 |
| [开发说明](docs/development.md) | 目录结构、构建、测试、CI |

## 致谢与许可

- 感谢 [@munmunjaklin458-afk](https://github.com/munmunjaklin458-afk) 开源的 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher)。
- 本项目基于 [MIT License](LICENSE) 开源。
