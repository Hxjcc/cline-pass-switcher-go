# Responses 协议边界与验收

该代理使用 Chat Completions 上游，提供无服务端会话存储的 Responses 兼容接口。

## 本轮修复

- namespace 下的 custom 工具在 added、done 和最终 output 中保留 namespace，多轮回放仍映射到同一个 Chat 工具名。
- 上游 `refusal` 字段以及内容数组中的 refusal 块生成 `response.refusal.delta/done`，最终内容保留 `type=refusal`。同一条消息混合文本与 refusal 时，content_index 连续、顺序不变。回放时使用 Chat 的 refusal 字段。
- 无法实现的能力在发送上游前返回 HTTP 400，错误包含 `type=invalid_request_error`、`code=unsupported_feature` 和具体 `param`。

明确不支持的能力包括：conversation 引用、后台响应、文件与音频内容、没有 URL/内联数据的图片引用、强制执行 web/file search 等服务端工具、强制执行 server 模式的 tool search、未知输入 item、不可读的外部压缩包和没有可读内容的外部加密思考。`previous_response_id` 继续要求客户端改为发送完整历史。

客户端会在普通问候请求里自动附带 `web_search` 等工具清单，因此“声明可用工具”不能视为“强制执行工具”。默认/auto/none 时，代理跳过已知不支持的服务端工具声明，并继续转发普通输入和支持的客户端工具；不据此宣称已提供搜索能力。显式 tool_choice 强制选中服务端工具，或者 required 且筛选后没有任何客户端工具时，仍返回 400。回归测试覆盖 tools[14]=web_search + 你好，并分别验证流式与非流式。

普通工具返回字符串是原始业务数据，不会因为字符串内部含有 `type=input_file` 等 JSON 字段就被当作协议拒绝。可读思考回放、普通图片/data URL、客户端 tool search 和本代理生成的压缩包仍受支持。custom grammar 仍通过工具描述交给 Chat 模型，未提供原生 grammar 约束采样。

协议参考：[OpenAI Responses 类型与事件](https://developers.openai.com/api/reference/cli/resources/responses)。

## 工具历史归一化

Chat Completions 要求每条 tool 消息都紧跟对应的 assistant tool_calls，而 ChatGPT Desktop 会把「没有对应调用」的工具结果写进会话历史，例如跨任务委派、历史裁剪或宿主侧工具的执行结果。默认行为是把这些孤儿结果转成用户消息继续转发，而不是让整轮请求失败：

- `create_thread` / `codex_app` 命名空间 / `<codex_delegation>` 开头的输出视为委派提示词，解包 `<input>` 后作为普通用户消息；
- 其它孤儿结果保留原文，并加上 `[tool result without a recorded call <name>]` 前缀，避免模型误当成用户发言；
- 若孤儿结果出现在同一批工具调用之间，会先缓冲、等这批调用全部有结果后再追加，保证 assistant → tool… → user 的顺序不被破坏；
- `strictToolHistory` 配置或 `STRICT_TOOL_HISTORY=true` 可以恢复旧的严格行为（返回 `orphan tool output` 错误）。

回归覆盖：`TestToChatToleratesOrphanToolOutputs`、`TestToChatStrictToolHistoryStillRejectsOrphans`、`TestOrphanToolOutputDoesNotSplitToolGroup`，以及端到端的 `TestDelegatedThreadHistoryReachesUpstream`。

## Web 搜索转发

ChatGPT Desktop 的 `web_search` 是托管工具：搜索由后端执行，客户端只负责声明。Chat Completions 上游没有这个概念，默认会被跳过。配置 `webSearchUpstream`（或环境变量 `WEB_SEARCH_UPSTREAM`）后，代理会把该声明替换成上游网关自己执行的服务端工具：

| 配置值 | 实际声明 |
|---|---|
| `exa` | `{"type":"vercel:exa_search"}` |
| `tako` | `{"type":"vercel:tako_search"}` |
| `perplexity` | `{"type":"vercel:perplexity_search"}` |
| `browserbase_fetch` | `{"type":"vercel:browserbase_fetch"}`（抓取指定 URL，不是搜索） |
| 留空 / `off` | 不映射（默认） |

另有 `webFetchUpstream`（环境变量 `WEB_FETCH_UPSTREAM`）：当用户消息里出现 http(s) 链接时，代理会额外声明 `{"type":"vercel:browserbase_fetch"}`，让模型能读取该链接的内容（实测可把 GitHub API 的字段原样读回，约 5 秒）。它只在检测到链接时声明，避免每轮请求都携带这类按次计费的工具；链接出现在工具输出而不是用户消息里时不会触发。

行为说明：搜索在网关侧执行，客户端会收到标准 `web_search_call` 生命周期；搜索调用显示搜索词，抓取调用显示打开的 URL。Exa 实际读取的页面列表不会返回，`url_citation` 之类的结构化引用也不会出现，来源仍以正文 URL 的形式给出。显式 `tool_choice` 强选 `web_search` 仍然返回 400，因为无法强制一个网关工具。这些 provider 工具按次计费，按需开启；`web_search_preview` 及其带日期的变体走同一条映射。

## 可重复验证

普通检查：

```sh
go test ./... -count=1
go vet ./...
go test -race ./... -count=1
```

Windows 上的 race 检测需要 CGO 与兼容的 C 编译器。本轮使用临时目录中的 w64devkit 2.10.0（GCC 16.2.0），仅在测试进程设置 `CGO_ENABLED=1` 和 `CC`，未修改全局 Go 配置或系统 PATH。下载文件的 SHA-256 与发布元数据一致：`18d0a4c71a166f8401ab6305781bec5882b40b5e06ba9807c61cb5f3b3c6325e`。[Go race 检测要求](https://go.dev/doc/articles/race_detector)

长流与共享流压测在本地生成数据，不调用真实模型：

```powershell
$env:CLINE_MANUAL_LONG_STREAM = '1'
$env:CLINE_STRESS_SECONDS = '60'
go test -race ./internal/httpapi -run 'Test(SharedStreamSoak|ManualStreamPastLegacy120sDeadline)$' -v -count=1 -timeout 4m
```

真实验收脚本接受 `--config`、`--codex`、`--workdir`、`--base-url` 和 `--model`。建议使用独立代理进程与配置副本，避免把测试请求混入常用实例。CLI 使用 `--ignore-user-config --ephemeral --sandbox workspace-write`，只操作验收目录内生成的固定文本和图片；随后独立通过 HTTP 验证 compact 与摘要回放。

CLI 文件/工具链路、CLI 自行压缩和 HTTP `/responses/compact` 是三个不同验收步骤。脚本记录真实请求中的工具名和图片块，并结合 CLI 的成功 `file_change` 与回读事件判断工具执行。`--auto-compact-limit` 可降低测试阈值以触发压缩；结果分别记录远端 compact 调用数和 CLI 本地压缩提示数，不能把两者混为一谈。

`--reuse-cli-evidence` 可以重新解析同一隔离目录下的既有 CLI 日志，再独立运行 HTTP compact/replay，报告明确标记证据复用。普通运行要求新目录，避免旧文件导致误判成功。

## 2026-09-18 验证记录

- `go test ./... -count=1`、`go vet ./...` 和 `go test -race ./... -count=1` 通过。
- 130 秒长流在 race 检测下正常结束，没有触发旧的 120 秒整体超时。
- 60 秒、8 worker 的共享流压测完成 31,368 轮，每轮覆盖断线重连、多个订阅、落盘和释放；结束时回放预算、文件、任务均为零，goroutine 从 2 回到 2。强制 GC 后堆保留量增加 411,816 字节，未见持续累积迹象。这是短时压力验收，不代替生产环境长期监控。
- 使用独立配置副本启动本轮编译的代理，在隔离目录中调用 Codex CLI 0.154.0 与 `cline-pass/glm-5.3-flash`。首次文件验收约 70 秒完成，得到 `COBALT-742 red` 并回读确认；独立 HTTP compact 后成功回忆标识、颜色和待办。
- 加入请求形状记录并把自动压缩阈值降到 18,500 后，CLI 用约 213 秒完成验收。请求中确认 `view_image` 和实际图片回传，CLI 日志确认成功文件变更和回读。该轮出现 9 次本地压缩提示，但没有调用远端 `/responses/compact`；自定义 provider 下使用普通 Responses 请求生成摘要后继续工具工作。因此这项通过的是 CLI 本地压缩后的工具链路，不是 CLI 自动调用本代理 compact 端点。
- 该低阈值测试多次重复摘要与工具尝试，耗时明显增加，不建议把测试阈值直接用作生产配置。模型也先尝试了不正确的 PowerShell 补丁命令，后来成功产生文件变更；这些失败保留在原始验收日志中。

真实模型输出仍可能不严格遵循指令。首次普通文本回忆测试中，上游除了回忆信息，还额外声称已经完成待办；没有实际工具调用支持这一说法，因此未把它计为文件操作通过。

后续独立 HTTP 验收请求了严格 JSON Schema。compact 成功且回放仍包含正确标识、颜色和待办，但真实上游返回 Markdown JSON 代码块，使用 `image_color` 替代要求的 `color`，并多出 `status` 字段。该次严格结构化输出验收未通过，不能据此声称该模型/渠道提供严格 schema 保证。当时代理只将 `text.format` 映射到 Chat 的 `response_format`。后续已增加下面的本地校验，使同类违规输出明确失败；历史验收记录保留原结果。

脱敏后的机器可读结果保存在本地 `docs/acceptance-2026-09-18.json`；`docs/acceptance-*.json` 按 `.gitignore` 不入库，该文件只存在于运行过验收的机器上。验收结束已停止隔离代理进程并删除包含账号密钥的临时配置副本，原始项目配置未修改。

## 严格 JSON Schema 结果校验

适用范围为 `/v1/responses`（以及 `/responses`、`/api/v1/responses`）上的 `text.format.type=json_schema`，并且有效 strict 设置为 true。沿用原有转换规则：省略 strict 时默认 true，显式 `strict:false` 不启用本地校验。普通文本、`json_object` 模式及 Chat Completions 透传接口不受此项调整影响。

schema 在发送上游前编译一次。使用固定版本 `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3`，默认 JSON Schema draft 2020-12，并开启 format 校验。支持嵌套对象/数组、required、additionalProperties、类型、枚举、数值/字符串约束、anyOf，以及 schema 内的 `$defs/$ref` 和递归引用。schema 的网络/本地文件加载全部禁用；所需定义应随请求提供。

- schema 本身无效或引用不可用：HTTP 400，`error.code=invalid_json_schema`，`param` 指明请求字段，不调用上游。
- 完整最终文本不符合 schema：非流式 HTTP 502；流式 `response.failed`；错误码统一为 `upstream_schema_validation_failed`，历史记录失败。
- SSE 增量仍实时发送，最终校验不会撤回已经发送的 delta。调用方必须等到 `response.completed` 才将结构化结果视为成功；失败时不得把之前的内容当作已验证数据。
- `refusal`、工具调用中间轮、length/content_filter 导致的 incomplete、网络错误和截断错误保留原有状态，不被 schema 错误覆盖。
- 本代理 compact 生成纯文本摘要，已清除结构化输出校验，避免把摘要当作 JSON。

校验不会去除 Markdown 代码块、重命名字段、删字段、转换值类型或重新生成答案。合规输出保留原始文本，包括空白和数字表示；大整数的比较不经 float64 舍入。

新增回归覆盖上述行为，并检查流式/非流式/普通 JSON 转 SSE 的终态、HTTP 状态、错误码、历史记录及“不自动重试”。依赖版本与校验和记录于 go.mod/go.sum，Docker 构建同步下载这些固定依赖。
