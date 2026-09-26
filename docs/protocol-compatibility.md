# Responses 协议兼容

Cline Pass 上游只提供 Chat Completions 接口。Codex 等客户端使用的是 OpenAI 的 Responses 协议，网关在 `/v1/responses` 上把请求转换成 Chat Completions，再把上游的结果（包括流式事件）转换回 Responses 格式。

网关不在服务端保存会话：每个请求都要带上完整的对话历史，响应中的 `store` 恒为 `false`。

## 请求转换

### 输入项

| 输入项 `type` | 处理方式 |
|---|---|
| `message`（或带 `role`、省略 `type` 的项）、纯字符串 | 转成对应角色的 Chat 消息。字符串视为用户消息。 |
| `reasoning` | 可读的思考内容作为上一条助手消息的 `reasoning_content` 回传给上游。Kimi / Moonshot 模型不回传。 |
| `function_call`、`custom_tool_call`、`tool_search_call` | 转成助手消息里的 `tool_calls`。 |
| `function_call_output`、`custom_tool_call_output`、`tool_search_output` | 转成 `tool` 消息；找不到对应调用时见[工具历史归一化](#工具历史归一化)。工具结果里的图片移到紧随其后的用户消息中。 |
| `compaction` | 解开网关生成的压缩内容，放回对话开头，见[上下文压缩](#上下文压缩)。 |
| `compaction_trigger` | 表示这一轮是 Codex 的远端压缩请求，见[上下文压缩](#上下文压缩)。 |
| `additional_tools` | 只读取其中的工具声明。 |

角色的处理：`developer` 转成 `system`。对话开始以后出现的 `system` 消息（Codex 会在历史中间插入环境提示）转成 `user`，因为不少上游会拒绝或丢弃位置靠后的系统消息。

### 内容类型

| 内容 | 处理方式 |
|---|---|
| `input_text`、`output_text`、`text` | 文本。 |
| `input_image` 等图片 | 支持 URL、`data:` URL，以及 base64 数据加 MIME 类型。`detail: "original"` 按 `high` 处理。 |
| `refusal` | 作为助手消息的 `refusal` 回传。 |

### 工具

| 客户端工具 | 发给上游的形式 |
|---|---|
| `function` | 普通函数工具，保留参数 Schema 和 `strict`。 |
| `custom` | 函数工具，只有一个字符串参数 `input`；原始定义（包括 grammar）附在工具描述里，上游不会按 grammar 约束采样。 |
| `namespace` 下的工具 | 函数工具，名称为 `命名空间__工具名`，超长时截断到 64 个字符；返回给客户端时还原成原来的命名空间和名称。 |
| 客户端执行的 `tool_search` | 名为 `tool_search` 的函数工具。 |
| `web_search`、`web_search_preview` 等 | 按 `WEB_SEARCH_UPSTREAM` 映射为网关执行的搜索工具，未配置时忽略；客户端用 `tool_choice` 强制选择它时翻译成 `tool_choice:"required"`（未配置搜索工具则返回 400），见[联网搜索与网页抓取](#联网搜索与网页抓取)。 |
| `file_search`、`code_interpreter`、`image_generation`、`computer*`、`mcp`、服务端执行的 `tool_search` | 忽略。 |

客户端经常在普通对话里也附带整份工具清单，所以"声明了"不等于"要求使用"：被忽略的工具不会导致请求失败。只有用 `tool_choice` **强制**选择一个无法转发的工具（可映射的 `web_search` 会转成 `tool_choice:"required"`），或者 `tool_choice` 为 `required` 但过滤后没有任何可用工具时，才返回 400。

`tool_choice` 支持 `auto`、`none`、`required`，以及指定一个 `function`、`custom` 或 `tool_search` 工具。请求里没有可用工具时，`tool_choice` 和 `parallel_tool_calls` 会被去掉。

### 推理与缓存

- `reasoning.effort` 映射到模型支持的推理档位：档位名完全匹配时直接使用；`xhigh`、`max` 这类更高的档位映射到模型支持的最接近档位。实际使用的档位记在响应头 `X-Cline-Reasoning-Effort` 和请求历史里。
- `prompt_cache_key` 原样转发，同时用于[会话粘性](routing.md#会话粘性)。

### 不支持的功能

以下请求在发给上游之前就返回 HTTP 400：

- `conversation` 引用、`background: true`。
- 消息或工具结果中的文件和音频内容（`input_file`、`input_audio` 等），以及只有 `file_id`、没有 URL 或内联数据的图片。
- 只有加密内容、没有可读文本的思考项；不是本网关生成、无法解读的压缩项。
- 未知的输入项类型、内容类型或工具类型（上面工具表以外的类型）；嵌套超过 16 层的工具命名空间。
- 强制使用无法转发的工具（见上文）。

错误格式：

```json
{
  "error": {
    "message": "background response execution is not supported by this Chat-backed proxy",
    "type": "invalid_request_error",
    "code": "unsupported_feature",
    "param": "background"
  }
}
```

`previous_response_id` 也会被拒绝（400，没有 `code` 和 `param`）：网关不保存历史响应，客户端需要改为每次发送完整历史。

## 响应转换

- 响应里的 `model` 始终是客户端请求的模型名，而不是上游返回的名称。
- 思考内容以 reasoning 输出项和 `response.reasoning_summary_text.*` 事件返回。DeepSeek 等提供可读思考过程的模型，另外发送 `response.reasoning_text.*` 事件。正文里夹带的 `<think>…</think>` 会被拆出来作为思考内容。
- 上游的 `refusal` 生成 `response.refusal.delta` / `response.refusal.done` 事件，最终输出里保留 `type: "refusal"` 的内容块。
- 网关自己执行的工具调用（联网搜索、网页抓取）不会出现在返回给客户端的输出里。

### 用量

返回给客户端的 `usage` 会按轮次折算：当网关在一次请求内部执行了多轮搜索或工具调用时，上游账单会把每一轮的输入 token 累加起来。网关把输入 token 和缓存 token 除以内部轮次数，并且不超过模型的上下文长度，避免 Codex 误以为上下文暴涨而提前压缩。输出 token 不折算。

请求历史里记录的是上游的原始用量和费用，所以两边的数字可能不同。

## 工具历史归一化

Chat Completions 要求每条工具结果紧跟在对应的工具调用之后。ChatGPT 桌面版和 Codex 的历史里经常有找不到调用的工具结果，例如跨任务委派、历史裁剪后留下的结果。默认情况下，网关把这类结果转成用户消息继续发送，而不是让整轮请求失败：

- 工具名为 `create_thread`、命名空间为 `codex_app`，或者内容以 `<codex_delegation>` 开头的结果，视为委派任务的提示词：取出其中 `<input>…</input>` 的内容，作为普通用户消息。
- 其他结果保留原文，前面加上 `[tool result without a recorded call <工具名>]`，避免模型误以为是用户说的话。
- 如果这样的结果夹在同一批工具调用之间，会等这批调用的结果都到齐后再追加，保证"助手调用 → 工具结果 → 用户"的顺序。
- 空的工具结果替换为 `(no output)`，因为有些上游不接受内容为空的工具消息。

设置 `STRICT_TOOL_HISTORY=true` 可以关闭这项处理，此时这类请求返回错误。

## 联网搜索与网页抓取

Responses 的 `web_search` 是由服务端执行的托管工具，Chat Completions 上游没有对应概念。配置 `WEB_SEARCH_UPSTREAM` 后，网关把客户端的 `web_search` 声明替换成 Cline 网关自己执行的搜索工具：

| 配置值 | 声明给上游的工具 |
|---|---|
| `exa` | `vercel:exa_search` |
| `tako` | `vercel:tako_search` |
| `perplexity` | `vercel:perplexity_search` |
| `vercel:` 开头的任意 ID | 原样使用 |
| 空、`off`、`none`、`false`、`disabled` | 不映射 |

启用搜索时，网关会在系统提示中附加一段搜索约束：每轮尽量只搜一次、最多取 3 条结果、不并行搜索。

`WEB_FETCH_UPSTREAM`（可选值 `browserbase_fetch`）控制网页抓取：只有当**用户消息**里出现 http(s) 链接时，网关才额外声明 `vercel:browserbase_fetch`，让模型读取链接内容。链接只出现在工具输出里时不会触发。

需要注意：

- 只有模型实际调用这些工具时才产生费用。上游返回的 `gateway_cost` 包含工具的按次费用，但实测 Cline Pass 账单只按 `cost`（模型费用）扣费；搜索结果会作为输入 token 进入模型，一次搜索可能带来上万输入 token，这部分按模型价格计费。
- 搜索在上游执行，客户端只看到最终回答，不会收到 `url_citation` 之类的结构化引用；来源会以正文中的链接给出。
- 用 `tool_choice` 强制使用 `web_search` 时，网关把它翻译成上游的 `tool_choice: "required"`，让这一轮必须调用搜索；没有配置搜索工具（`WEB_SEARCH_UPSTREAM=off` 等）则返回 400。

## Windows shell 兼容

Codex 的命令执行工具带有一个 `shell` 参数。模型漏填时，Windows 上的客户端会回退到 `cmd.exe`。

- `SHELL_COMPAT=powershell`：在发给模型的工具定义里，把 `shell` 参数限定为 `powershell` 并设为必填。只影响本身带 `shell` 参数的工具。
- `SHELL_COMPAT_ENFORCE=true`：再进一步，在模型输出的工具调用参数完整之后，把其中的 `shell` 改写（或补上）为 `powershell`。流式请求会先缓冲这类工具调用的参数，完整后再发出。只在 `SHELL_COMPAT` 开启时生效。

## 结构化输出校验

请求使用 `text.format.type = "json_schema"` 且 `strict` 为 `true`（省略时视为 `true`）时，网关除了把 Schema 转发给上游，还会在本地校验最终输出。

- 校验器为 `github.com/santhosh-tekuri/jsonschema/v6`，按 JSON Schema draft 2020-12 并开启 format 校验，支持 Schema 内部的 `$defs` / `$ref`，不加载任何外部引用。
- Schema 本身无效：返回 400（`invalid_json_schema`），`param` 指出字段，不调用上游。
- 最终输出不符合 Schema：非流式返回 502（`upstream_schema_validation_failed`）；流式以 `response.failed` 事件结束，错误码相同。这类失败不会自动重试。
- 流式请求的增量内容照常实时发送，校验只在结束时进行，不会撤回已经发出的内容。调用方必须等到 `response.completed` 才能把结果当作合格的结构化数据。
- 网关不会修正输出：不去掉 Markdown 代码块、不改字段名、不转换类型。合格的输出保留原文。
- 工具调用、拒答、因长度或内容过滤而不完整的输出不参与校验，保留原来的状态。
- `strict: false` 时只转发 Schema，不在本地校验。

## 上下文压缩

对话接近上下文上限时，Codex 会让服务端把较早的历史压缩成一段摘要。网关提供两个入口：

| 入口 | 返回 |
|---|---|
| `POST /v1/responses`，输入中带 `{"type": "compaction_trigger"}` | 远端压缩 v2：正常的 Response，输出里恰好有一个 `compaction` 项。Codex 的 `/compact` 和自动压缩走这条路。 |
| `POST /v1/responses/compact` | 独立接口，返回 `response.compaction` 对象，供脚本和自定义客户端使用。 |

两者都以非流式方式请求上游；客户端要求流式时，网关在本地生成对应的事件序列。请求历史里的类型为 `compact`。

### 让 Codex 使用远端压缩

Codex 只对 OpenAI 和 Azure OpenAI 形态的 provider 启用远端压缩，其他自定义 provider 会退回"本地摘要"：客户端自己发一次普通请求让模型总结。要让 Codex 使用网关的远端压缩，把 provider 的 `name` 设为 `azure`（大小写不敏感）：

```toml
[model_providers.cline-pass]
name = "azure"
base_url = "http://127.0.0.1:3123/v1"
wire_api = "responses"
```

### 压缩内容

压缩结果由两部分组成：

1. **摘要**，覆盖较早的对话。网关要求模型按四个固定段落输出：`## Objective`、`## Work State`、`## Next Move`、`## Relevant Files`。
2. **最近的原文**，默认约 16000 token（`COMPACTION_RECENT_TOKENS`），只包含用户和助手的文字，单条最多 2000 字符、总计最多 32000 字符。这样最近的路径、命令和报错不会在摘要中被改写。

两部分一起放在 `compaction` 项的 `encrypted_content` 中，以 `ocx1:` 开头、base64 编码。客户端下一轮把它带回来时，网关按「摘要 → 最近原文 → 本轮新输入」的顺序放回对话。

### 预算与重试

- 压缩请求使用 `COMPACTION_REASONING_EFFORT` 指定的推理档位（默认 `max`），输出预算不低于 `COMPACTION_MIN_OUTPUT_TOKENS`（默认 16384）。
- 推理模型有时把预算全部用在隐藏的思考上，最后没有输出摘要（上游返回 `empty response content`）。遇到这种情况，网关改用模型的最高推理档位、把输出预算加倍（最多 32768）重试一次。两次尝试都记在请求历史里。
- 其他失败已经经过正常的账号和渠道重试，不再额外重试。

### 降级与提示

- **压缩降级。** 摘要最终没能生成时，网关不返回错误，而是返回一个降级的 `compaction` 项，其中包含失败原因、已经产生的部分摘要和最近的用户请求。会话可以继续，代价是较早的上下文丢失。请求历史中这条记录标为「压缩降级」。
- **摘要缺段。** 摘要正常结束但缺少某些段落时（例如只有 Objective 和 Work State），压缩照常生效，不重试；请求历史的模型列会显示「摘要缺 …」，列出缺少的段落。

## 流式输出

- 等待上游数据期间，网关每 10 秒向客户端发送一次 `: ping` 注释行保活。
- 单个 SSE 事件最大 8 MiB，超过时流以错误结束（`stream_event_too_large`）。整条流的总时长和总长度不设上限。
- 上游的流不完整时，网关以 `response.failed` 结束并记录原因，而不是报告成功。常见的错误码：`stream_truncated`（没有结束标记就断开）、`upstream_final_output_missing`（结束了但没有正文或工具调用）、`upstream_tool_call_dropped` / `upstream_tool_call_missing`（工具调用不完整）。
- 一个 Responses 流式请求的上游流还没结束时，又来了内容完全相同的请求（例如客户端断线后立即重连），新请求会接入同一个上游流、从头回放，而不是再请求一次上游，见[运维说明 · 流共享与回放](operations.md#流共享与回放)。
