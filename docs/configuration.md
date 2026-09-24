# 配置参考

本文列出网关的全部配置项：环境变量名、`config.json` 字段、默认值、取值范围，以及它们之间的优先级。

## 配置从哪里来

启动时按下面的顺序得到最终配置，后一步覆盖前一步：

1. 内置默认值。
2. 数据目录下的 `config.json`。文件不存在时使用默认值，并在启动时写出一份。
3. 环境变量。只有**非空且能解析**的值才会覆盖；空字符串、非法数字、无法识别的布尔值都会被忽略，保留前两步的结果。

有两条规则容易踩坑：

- **控制台保存会把整份生效配置写回 `config.json`，包括来自环境变量的值。** 因此删掉一个环境变量，并不会让对应设置恢复默认：`config.json` 里已经留下了之前的值。要关闭某个开关，请用一个明确的值覆盖它（例如 `WEB_SEARCH_UPSTREAM=off`、`SHELL_COMPAT_ENFORCE=false`），或者停止服务后手动修改 `config.json`。
- **环境变量只在启动时读取。** 修改后需要重启进程；用 Docker Compose 时执行 `docker compose up -d` 重建容器。

手动编辑 `config.json` 前请先停止服务。运行中的进程会在下一次保存时覆盖文件；进程异常退出后，启动时重放的操作日志（见[运维说明](operations.md#数据目录)）也可能覆盖手动改动。

### 查看当前生效值

控制台「访问与安全 → 运行参数（当前生效值）」列出了最常需要确认的几项，同样的数据可以通过 `GET /api/settings` 读取。每一行都标出来源：

| 来源 | 含义 |
|---|---|
| `env` | 来自环境变量，且环境变量的值确实产生了当前值 |
| `config` | 启动时 `config.json` 里写有这个字段 |
| `default` | 以上都没有，使用内置默认值 |
| `builtin` | 代码常量，不可配置 |

`config` 的判断依据是**启动时** `config.json` 里出现过哪些字段。控制台保存过一次之后，多数字段都会被写进文件，重启后来源就会显示为 `config`，即使你从未手动设置过它们。

## 运行环境

这几项只能通过环境变量设置，不会写入 `config.json`。

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `DATA_DIR` | `.`（Docker 镜像中为 `/data`） | 数据目录，存放 `config.json`、`metadata.json`、`store.journal` 和锁文件。不存在时自动创建。 |
| `BIND_HOST` | `127.0.0.1`（Docker 镜像中为 `0.0.0.0`） | 监听地址。 |
| `CLINE_PASS_KEY` | 无 | 启动时把这个 Cline Pass API Key 作为名为 `env-account` 的账号插到账号池最前面；账号池里已有相同 Key 时不重复添加。之后它就是一个普通账号，控制台保存时会写进 `config.json`。 |
| `PUID` / `PGID` | `10001` | 仅 Docker 镜像使用：入口脚本以 root 修正数据目录的属主，然后切换到这个用户运行。 |

程序没有命令行参数。

## 监听与访问控制

| 环境变量 | `config.json` 字段 | 默认值 | 说明 |
|---|---|---|---|
| `PORT` | `port` | `3123` | 监听端口，取值 1–65535，超出范围时回到 `3123`。控制台不能修改。 |
| `PROXY_KEY` | `proxyKey` | 空 | 代理主密钥。设置后，模型接口要求 `Authorization: Bearer <密钥>`。未设置 `ADMIN_KEY` 时，它同时是控制台的登录密钥。 |
| `ADMIN_KEY` | `adminKey` | 空 | 管理密钥。设置后，控制台和 `/api/*` 只接受这个密钥，`PROXY_KEY` 和签发的代理密钥都不能再打开控制台。留空时沿用 `PROXY_KEY`。 |
| `PUBLIC_BASE_URL` | `publicBaseUrl` | 空 | 对外访问地址，例如 `https://cline.example.com`，末尾的 `/` 会被去掉。控制台显示的接入地址和启动日志都以它为准。放在做 HTTPS 终结的反向代理后面时**必须设置**，否则控制台的保存操作会被拒绝（HTTP 403），原因见[访问控制](operations.md#访问控制)。 |
| `TRUSTED_PROXIES` | `trustedProxies` | 空 | 可信反向代理的 IP 或 CIDR，逗号分隔。来自这些地址的 `X-Forwarded-For` / `X-Real-IP` 会被采信，用于两件事：没有设置密钥时判断请求是否来自本机；认证失败限流按真实客户端 IP 分别计数。只填反代实际使用的地址，不要填整段私网网段。控制台不能修改。 |
| `TRUST_LOCAL_PORT_FORWARD` | `trustLocalPortForward` | `false`（Compose 中为 `1`） | 声明"非回环地址的连接只可能来自宿主机回环端口映射"。只在某个接口没有设置密钥时起作用：这类连接会被当作本机访问，但仍要求 Host 头是 `localhost` 或回环 IP。端口一旦对外暴露，就必须关闭它并设置密钥。控制台不能修改。 |

控制台「访问与安全」页可以修改 `proxyKey`、`adminKey` 和 `publicBaseUrl`；环境变量设置了同名项时，重启后仍以环境变量为准。

签发给他人的客户端密钥保存在 `proxyKeys` 字段，由控制台「代理密钥」页管理，见下文 [`proxyKeys`](#proxykeys)。

## 协议兼容

这一组开关影响 Responses 接口（Codex 使用的协议）如何转换请求，行为细节见 [Responses 协议兼容](protocol-compatibility.md)。控制台都不能修改。

| 环境变量 | `config.json` 字段 | 默认值 | 说明 |
|---|---|---|---|
| `STRICT_TOOL_HISTORY` | `strictToolHistory` | `false` | 开启后，历史里找不到对应调用的工具结果会直接报错，而不是转成用户消息继续发送。 |
| `WEB_SEARCH_UPSTREAM` | `webSearchUpstream` | 空（Compose 中为 `exa`） | 客户端声明 `web_search` 工具时，改为声明 Cline 网关执行的搜索工具。可选 `exa`、`tako`、`perplexity`，也可以直接写 `vercel:` 开头的网关工具 ID；`off`、`none`、`false`、`disabled` 或空值表示关闭。由上游按次计费。 |
| `WEB_FETCH_UPSTREAM` | `webFetchUpstream` | 空（Compose 中为 `browserbase_fetch`） | 用户消息里出现 http(s) 链接时，额外声明网关的网页抓取工具。可选 `browserbase_fetch`（`browserbase`、`fetch`、`on`、`true` 等价），也可以写 `vercel:` 开头的 ID；关闭方式同上。由上游按次计费。 |
| `SHELL_COMPAT` | `shellCompat` | 空 | 把转发给模型的工具定义里的 `shell` 参数限定为这个值并设为必填，例如 Windows 客户端设为 `powershell`。`off` 等值或空值表示关闭。 |
| `SHELL_COMPAT_ENFORCE` | `shellCompatEnforce` | `false` | 在 `SHELL_COMPAT` 开启时，把模型实际输出的工具调用参数里的 `shell` 也改写为该值。 |

## 上下文压缩

这一组参数控制 Codex 远端压缩时网关如何生成摘要，机制见 [Responses 协议兼容 · 上下文压缩](protocol-compatibility.md#上下文压缩)。控制台都不能修改。

| 环境变量 | `config.json` 字段 | 默认值 | 取值 | 说明 |
|---|---|---|---|---|
| `COMPACTION_RECENT_TOKENS` | `compactionRecentTokens` | `16000` | 0–64000 | 压缩时原样保留的最近对话量（估算 token）。摘要只覆盖更早的部分。`0` 表示不保留原文。 |
| `COMPACTION_REASONING_EFFORT` | `compactionReasoningEffort` | `max` | `auto`、`max`、`xhigh`、`high`、`medium`、`low`、`minimal`、`none` | 压缩请求使用的推理档位，会映射到模型实际支持的档位；`auto` 取最接近 `high` 的一档。无法识别的值按 `max` 处理。 |
| `COMPACTION_MIN_OUTPUT_TOKENS` | `compactionMinOutputTokens` | `16384` | 1024–32768 | 压缩请求的输出预算下限。小于 1024 时回到 `16384`，大于 32768 时按 32768 计；实际下限不低于 4096。 |

摘要因推理耗尽预算而失败时，网关会用模型的最高推理档位、最多 32768 的输出预算重试一次。这个 32768 是代码常量，在运行参数表中显示为 `builtin`。

## 仅在 config.json 中的字段

下面这些字段没有对应的环境变量，一般通过控制台修改。

| 字段 | 默认值 | 控制台位置 | 说明 |
|---|---|---|---|
| `upstreamBase` | `https://api.cline.bot/api/v1` | 无 | Cline Pass 上游地址，末尾 `/` 会被去掉。 |
| `accounts` | `[]` | 账号池 | 账号列表，每项包含 `id`、`name`、`key`、`enabled`。`enabled` 缺省为 `true`；缺少 `id` 时自动生成。 |
| `accountMode` | `single` | 账号池 | `single`（单账号）或 `roundrobin`（账号池轮询），其他值按 `single` 处理。 |
| `activeAccount` | `0` | 账号池 | 单账号模式下「当前账号」在列表中的下标，超出范围时自动收回到有效范围。 |
| `knownModels` | 内置的 15 个模型 | 模型与上游 | 订阅模型列表，也就是 `/v1/models` 返回的内容。 |
| `removedModels` | `[]` | 模型与上游 | 手动移除过的模型。「拉取官方模型」不会把它们加回来。 |
| `perModel` | `{}` | 模型与上游（展开模型行） | 每个模型的渠道偏好，见下文。 |
| `proxyKeys` | `[]` | 代理密钥 | 签发的客户端密钥，见下文。 |
| `apiKey` | — | 无 | 旧版字段。账号池为空时会被迁移成一个名为「默认账号」的账号，随后从配置中删除。 |

### perModel

以模型 ID 为键，值包含以下字段：

| 字段 | 说明 |
|---|---|
| `upstreams` | 优先使用的渠道，按顺序尝试，最多 10 个。出现在 `exclude` 里的渠道会被移除。 |
| `exclude` | 排除的渠道，最多 10 个。 |
| `pinMode` | `strict`（默认）或 `preferred`。`strict` 每次只允许一个渠道；`preferred` 把当前渠道排在最前，其余优先渠道作为网关的备选。 |
| `sort` | `cost`、`ttft`、`tps` 或 `null`。没有指定优先渠道时，让网关按成本、首字延迟或吞吐选择渠道。 |
| `upstream` | 旧版的单渠道字段，读取时并入 `upstreams`。 |

这些设置怎样影响请求，见[账号与渠道调度](routing.md#渠道钉住排除与排序)。

### proxyKeys

每个签发的客户端密钥包含：

| 字段 | 说明 |
|---|---|
| `id` | 自动生成，形如 `key_` 加 16 位十六进制。用量统计按它记账，改名不影响。 |
| `name` | 名称，出现在请求历史里。 |
| `key` | 密钥本身。控制台随机生成的格式是 `sk-` 加 48 位十六进制。 |
| `enabled` | 是否启用，缺省为 `true`。禁用的密钥请求时返回 403。 |
| `accountId` | 绑定的账号 ID。留空表示使用整个账号池。 |
| `spendLimitUsd` | 额度上限（美元），`0` 表示不限。 |
| `note` / `createdAt` | 备注与创建时间，仅供展示。 |

最多 100 个。`key` 为空、与 `proxyKey` 或 `adminKey` 相同、或与其他密钥重复的条目会在保存时被丢弃。绑定账号和额度上限的具体行为见 [HTTP 接口 · 代理密钥](api.md#代理密钥与额度)。

## Docker Compose 的默认值

仓库自带的 `docker-compose.yml` 由仓库维护，请不要直接修改，否则每次 `git pull` 都会冲突。本地配置写在同目录的 `.env` 文件里，Compose 会自动读取。

Compose 与程序默认值不同的地方：

| 项 | Compose 中的值 | 说明 |
|---|---|---|
| `DATA_DIR` | `/data` | 映射到宿主机的 `./data`。 |
| `BIND_HOST` | `0.0.0.0` | 容器内监听所有地址；宿主机端口只绑定在 `127.0.0.1:3123`。 |
| `TRUST_LOCAL_PORT_FORWARD` | `1`（固定） | 因为端口只映射到宿主机回环地址。 |
| `WEB_SEARCH_UPSTREAM` | `exa` | `.env` 里未定义时生效。 |
| `WEB_FETCH_UPSTREAM` | `browserbase_fetch` | 同上。 |
| `SHELL_COMPAT_ENFORCE` | `false` | 总会传入，所以运行参数表中它的来源显示为 `env`。 |

Compose 只把以下变量从 `.env` 传进容器：`PROXY_KEY`、`ADMIN_KEY`、`PUBLIC_BASE_URL`、`TRUSTED_PROXIES`、`WEB_SEARCH_UPSTREAM`、`WEB_FETCH_UPSTREAM`、`SHELL_COMPAT`、`SHELL_COMPAT_ENFORCE`、`COMPACTION_RECENT_TOKENS`、`COMPACTION_REASONING_EFFORT`、`COMPACTION_MIN_OUTPUT_TOKENS`，外加入口脚本读取的 `PUID`、`PGID`。`PORT`、`CLINE_PASS_KEY`、`STRICT_TOOL_HISTORY` 写在 `.env` 里不会生效；需要时请停止容器，改 `data/config.json` 里的对应字段。

一个对外部署的 `.env` 示例：

```bash
ADMIN_KEY=换成一段足够长的随机字符串
PROXY_KEY=换成另一段随机字符串
PUBLIC_BASE_URL=https://cline.example.com
# 宿主机上的反向代理经端口映射访问容器时，容器看到的来源地址
TRUSTED_PROXIES=172.18.0.1
# 关闭按次计费的联网工具时，写 off，不要留空
WEB_SEARCH_UPSTREAM=off
WEB_FETCH_UPSTREAM=off
```

`TRUSTED_PROXIES` 应填容器网络的网关地址，可以这样查：

```bash
docker inspect cline-pass-console --format '{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}'
```
