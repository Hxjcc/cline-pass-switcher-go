# HTTP 接口

网关提供两组接口：给客户端用的 OpenAI 兼容模型接口，以及给控制台用的管理接口 `/api/*`。两组接口使用不同的密钥。

## 密钥

| 密钥 | 设置位置 | 可以访问 |
|---|---|---|
| 代理主密钥 `PROXY_KEY` | 环境变量或「访问与安全」页 | 模型接口；未设置管理密钥时也能登录控制台 |
| 管理密钥 `ADMIN_KEY` | 环境变量或「访问与安全」页 | 控制台和管理接口 |
| 签发的代理密钥 | 「代理密钥」页 | 只能访问模型接口，可绑定账号、设置额度上限 |

某一组接口**没有设置任何密钥**时，它只接受本机访问：连接必须来自回环地址（或经 `TRUSTED_PROXIES`、`TRUST_LOCAL_PORT_FORWARD` 声明为本机的来源），并且 Host 头必须是 `localhost` 或回环 IP。其他请求返回 403（`access_error`）。详见[运维说明 · 访问控制](operations.md#访问控制)。

## 模型接口

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/v1/models` | 订阅模型列表 |
| `POST` | `/v1/chat/completions` | Chat Completions |
| `POST` | `/v1/responses` | Responses，Codex 使用 |
| `POST` | `/v1/responses/compact` | 独立的上下文压缩接口 |

每个路径都有两个等价别名：去掉 `/v1` 的形式（如 `/chat/completions`），以及加上 `/api` 前缀的形式（如 `/api/v1/chat/completions`）。

认证方式为 `Authorization: Bearer <密钥>`，可以使用代理主密钥或签发的代理密钥。

### GET /v1/models

返回订阅模型列表，与控制台「模型与上游」页一致：

```json
{
  "object": "list",
  "data": [{ "id": "cline-pass/glm-5.3-flash", "object": "model" }],
  "models": []
}
```

`models` 恒为空数组，供新版 Codex 客户端解析。

### POST /v1/chat/completions

请求基本原样转发给 Cline Pass，网关只做这些处理：

- 按账号池和渠道设置选择账号与渠道，失败时重试，见[账号与渠道调度](routing.md)。
- 请求带有推理参数（`reasoning_effort` / `reasoning`），或者模型支持推理时，自动加上 `include_reasoning: true`，除非请求里已经写明。
- 响应里的思考内容同时以 `reasoning` 和 `reasoning_content` 两个字段提供，兼容不同客户端。
- 上游忽略 `stream: true`、直接返回完整结果时，网关把它转成一段合法的 SSE 流。

非流式请求失败时，返回最后一次上游尝试的状态码和响应体。流式请求在开始输出前失败时同样如此；开始输出后出错，流会直接结束。

### POST /v1/responses 与 /v1/responses/compact

网关把 Responses 请求转换成 Chat Completions 发给上游，再把结果转换回 Responses 格式。支持范围、限制和压缩机制见 [Responses 协议兼容](protocol-compatibility.md)。

响应头 `X-Cline-Reasoning-Effort` 给出实际发给上游的推理档位。

### 响应头

模型接口的响应带有 `X-Cline-*` 诊断头，列表见[账号与渠道调度 · 响应头](routing.md#响应头)。

## 代理密钥与额度

在「代理密钥」页可以签发多个客户端密钥，分给不同的人或用途，最多 100 个。每个密钥可以单独启用或停用，并可设置两项限制。

**绑定账号。** 使用该密钥的请求只走绑定的账号，出错时不会改用其他账号。绑定的账号被停用或删除后，请求返回 429（`key_spend_limit`，消息说明绑定账号不可用）。

**额度上限（美元）。** 网关把上游在响应中报告的费用累加到该密钥名下，累计值达到上限后拒绝新请求。

- 只累计上游报告的费用（`usage.cost`），与 Cline Pass 账单一致。上游还会返回 `gateway_cost` / `market_cost`，其中包含联网搜索等工具的按次费用，但账单不按它们扣费，所以不计入。上游没有返回费用的请求按 0 计，网关不做估算。
- 客户端中途取消的流式请求拿不到最终费用，按 0 计，但上游仍会照常扣费，所以累计值可能比实际偏低。
- **并发预留。** 同一个密钥有请求正在进行时，新请求准入前会按该密钥的历史平均单次费用，为进行中的每个请求预留一份。「已用 + 预留」达到上限时拒绝新请求，等进行中的请求结束后即可重试。预留只用于准入判断，不计入已用额度。
- 累计值保存在 `metadata.json` 的 `keyUsage` 里，重启不会清零。可以在「代理密钥」页清零，或调用 `POST /api/keys/reset`。

主密钥和控制台发出的请求不计入任何密钥的额度。

被拒绝时的响应：

| 情况 | 状态码 | `error.code` | 响应头 `X-Cline-Key-Limit` |
|---|---|---|---|
| 已用额度达到上限 | 429 | `key_spend_limit` | `exceeded` |
| 已用加预留达到上限 | 429 | `key_spend_limit` | `reserved` |
| 绑定的账号不可用 | 429 | `key_spend_limit` | `exceeded` |
| 密钥已停用 | 403 | `key_disabled` | — |

```json
{
  "error": {
    "message": "该代理密钥的额度已用尽（限额 $10，已用 $10.0213），请联系管理员提额或改用新密钥",
    "type": "insufficient_quota",
    "code": "key_spend_limit"
  }
}
```

## 认证失败限流

为防止猜测密钥，同一来源在 1 分钟内用错密钥 5 次后会被暂时拒绝：第一次 30 秒，之后每次触发翻倍，最长 15 分钟。

- 拒绝期间即使密钥正确也会被拒绝，返回 429（`type` 为 `rate_limit_error`，`code` 为 `auth_throttled`），并带 `Retry-After` 头。
- 请求里**完全没带密钥**不计入失败次数，只返回 401，这样控制台在登录前的探测请求不会把管理员自己锁住。
- 模型接口和管理接口分开计数。
- 认证成功会清空该来源的失败计数。计数只在内存中，重启后清零。

"同一来源"默认按连接的对端 IP 区分。连接来自本机回环地址或 `TRUSTED_PROXIES` 中的地址，并且带有 `X-Forwarded-For` / `X-Real-IP` 时，改按其中记录的真实客户端 IP 区分。放在反向代理后面却没有配置 `TRUSTED_PROXIES` 时，所有客户端共用反代的地址：一个客户端反复用错密钥，会让同一反代后面的所有人一起被拒绝。

## 管理接口

认证方式为请求头 `X-Admin-Key: <管理密钥>`，也接受 `Authorization: Bearer <管理密钥>`。未设置 `ADMIN_KEY` 时，管理密钥就是 `PROXY_KEY`。

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/meta` | **无需认证。** 返回 `authRequired`（是否设置了管理密钥）和 `configured`（是否有可用账号）；认证通过时还返回 `proxyBase`。 |
| `GET` | `/api/models` | 订阅模型及其渠道设置、探测信息。 |
| `POST` | `/api/models/remove` | 移除订阅模型。请求体 `{"model": "..."}`。 |
| `POST` | `/api/fetch-official-models` | 从公开来源拉取 Cline Pass 模型列表，把新模型加入订阅。 |
| `POST` | `/api/probe` | 探测模型的渠道与路由方式。请求体 `{"model": "..."}`。**产生费用。** |
| `POST` | `/api/validate-upstreams` | 逐个校验模型的候选渠道。请求体 `{"model": "..."}`。**产生费用。** |
| `POST` | `/api/test` | 发送一次测试请求。请求体 `{"model": "...", "upstreams": [...], "exclude": [...]}`，后两项可省略，省略时使用已保存的设置。**产生费用。** |
| `GET` / `POST` | `/api/config` | 读取配置摘要；保存模型渠道设置，请求体 `{"perModel": {"<模型>": {...}}}`。 |
| `GET` / `POST` | `/api/accounts` | 读取账号池（`?reveal=1` 返回完整 API Key）；保存账号池，请求体 `{"accounts": [...], "mode": "single", "active": 0}`。 |
| `POST` | `/api/accounts/test` | 测试账号，请求体 `{"key": "..."}` 或 `{"id": "..."}`。**产生费用。** |
| `GET` | `/api/accounts/quota` | 读取各账号的套餐用量。`?refresh=1` 跳过缓存，`?id=` 只查一个账号。 |
| `GET` / `POST` | `/api/keys` | 读取签发的代理密钥（`?reveal=1` 返回完整密钥）；整体保存密钥列表。 |
| `POST` | `/api/keys/reset` | 清零额度用量。请求体 `{"id": "..."}` 或 `{"all": true}`。 |
| `GET` / `POST` | `/api/security` | 读取或保存 `proxyKey`、`adminKey`、`publicBaseUrl`。 |
| `GET` | `/api/settings` | 运行参数的当前生效值及来源，见[配置参考](configuration.md#查看当前生效值)。 |
| `GET` | `/api/history` | 请求历史，见下文。 |
| `POST` | `/api/history/clear` | 清空请求历史。账号的请求计数和密钥用量不受影响。 |

### GET /api/history

| 参数 | 说明 |
|---|---|
| `limit` | 每页条数，默认 50，最大 200。 |
| `offset` | 跳过的条数。 |
| `q` | 关键字，匹配模型、渠道、规范模型、账号、类型、推理档位和错误信息。 |
| `result` | `error` 只看失败，`ok` 只看成功。 |

记录按时间从新到旧排列，最多保留 500 条。每条记录包含时间、模型、实际渠道、耗时、首字延迟、token 用量与费用、错误信息、账号、使用的代理密钥，以及每次上游尝试的渠道、状态码和耗时。`kind` 字段表示请求类型：`chat`、`responses`、`compact`（上下文压缩）或 `test`（控制台测试）。客户端中途断开的请求，错误信息记为「客户端取消」。

## 其他

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/healthz` | 健康检查，无需认证，返回 `{"ok": true}`。 |
| `GET` | 其他路径 | 控制台页面。 |

- 请求体最大 50 MiB。
- `/api` 和 `/v1` 下不存在的路径返回 404 的 JSON 错误，不会返回控制台页面。
- 网关不提供 CORS 支持。`OPTIONS` 请求返回 204，但不带 `Access-Control-*` 头；来自其他站点的浏览器请求会被拒绝（403）。

## 错误格式

网关自己产生的错误使用 OpenAI 风格的结构：

```json
{ "error": { "message": "...", "type": "...", "code": "...", "param": "..." } }
```

`code` 和 `param` 只在有意义时出现。常见的错误：

| 状态码 | `type` | `code` | 场景 |
|---|---|---|---|
| 400 | `invalid_request_error` | `unsupported_feature` | Responses 请求用到了不支持的功能，`param` 指出字段。 |
| 400 | `invalid_request_error` | `invalid_json_schema` | 结构化输出的 JSON Schema 无效。 |
| 400 | `invalid_request_error` | `no_allowed_channels` / `channel_list_unknown` | 渠道排除规则无法满足。 |
| 401 | `auth_error` | — | 密钥缺失或错误。 |
| 403 | `auth_error` | `key_disabled` | 签发的密钥已停用。 |
| 403 | `access_error` | — | 没有设置密钥时的非本机访问，或者跨站请求。 |
| 429 | `rate_limit_error` | `auth_throttled` | 认证失败次数过多。 |
| 429 | `insufficient_quota` | `key_spend_limit` | 代理密钥额度不足或绑定账号不可用。 |
| 502 | `upstream_error` | `upstream_schema_validation_failed` | 模型输出不符合请求的 JSON Schema。 |
| 503 | `configuration_error` | `no_account` | 账号池里没有可用账号。 |

上游返回的错误会尽量保留上游的状态码和错误信息。
