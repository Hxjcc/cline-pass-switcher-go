# Cline Pass Switcher

本地 OpenAI 兼容代理，把 Cline、Codex 等客户端接到 [Cline Pass](https://cline.bot/)，并提供 Web 控制台管理账号、渠道和请求记录。

这是 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher) 的 Go 重写版：配置字段和 API 路径保持兼容，已有的 `config.json` / `metadata.json` 可以直接拿来用。

## 能做什么

- 在浏览器里管理账号、模型和渠道，查看请求轨迹
- 单账号或账号池轮询；账号 401 / 403 / 429 后自动冷却并切换
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
| `PUBLIC_BASE_URL` | 控制台展示给客户端的公网地址 |
| `PORT` | 监听端口，默认 `3123` |
| `BIND_HOST` | 监听地址，本地默认 `127.0.0.1`，容器内应设为 `0.0.0.0` |
| `DATA_DIR` | 配置和元数据目录，容器内为 `/data` |

Docker Compose 已经设置了 `DATA_DIR` 和 `BIND_HOST`。镜像里的 `PORT=3123` 会覆盖 `config.json` 中的端口，与端口映射保持一致。

挂在 nginx、Traefik 等反向代理后面时，保持本机绑定即可。流式输出需要关闭响应缓冲，并加大读取超时。

## 安全

- `config.json`、`metadata.json` 和 `data/` 里可能有明文密钥，不要提交到 Git。
- 对公网开放前务必设置代理密钥（`proxyKey` / `PROXY_KEY`）。
- 控制台里的探测、测试、校验会向真实上游发小额请求。

## 致谢

感谢原作者 [@munmunjaklin458-afk](https://github.com/munmunjaklin458-afk) 开源 [cline-pass-switcher](https://github.com/munmunjaklin458-afk/cline-pass-switcher)。

## License

[MIT](LICENSE)
