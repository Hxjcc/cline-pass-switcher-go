# 运维说明

本文说明部署到服务器以后需要了解的内容：数据怎样保存、访问控制怎样判断、反向代理怎样配置、流式请求占用多少资源，以及启动、退出和升级。

## 数据目录

所有状态都保存在 `DATA_DIR` 指定的目录里（源码运行默认是当前目录，Docker 中是 `/data`，映射到宿主机的 `./data`）。

| 文件 | 内容 |
|---|---|
| `config.json` | 配置：账号池、密钥、签发的代理密钥、订阅模型、每个模型的渠道设置等。 |
| `metadata.json` | 运行数据：模型的探测结果与能力信息、最近 500 条请求历史、每个账号的请求计数、每个代理密钥的累计用量（`keyUsage`）。 |
| `store.journal` | 操作日志。每次配置修改和请求记录都先追加到这里，再定期合并进上面两个文件。 |
| `.store.lock` | 锁文件，防止两个进程同时使用同一个数据目录。 |

`config.json` 和 `store.journal` 里有账号的 API Key 和各类密钥，请限制这个目录的访问权限，不要提交到代码仓库。

### 写入与持久性

- **配置修改**（保存账号、密钥、模型设置、移除模型、清空历史等）会先把日志写入磁盘（fsync），再返回成功，并立即重写两个 JSON 文件。
- **请求记录**每条都追加到日志，但每 32 条才 fsync 一次。进程崩溃不会丢记录，重启时会从日志重放；断电最多丢失最近的少量请求记录。
- **模型探测结果**只写日志并 fsync，不立即重写 JSON 文件；批量探测不会反复重写 `metadata.json`。
- 日志累计 100 条未合并的操作时，合并一次进 JSON 文件并清空日志。正常退出时也会合并。
- 一旦写日志失败，之后的所有修改都会失败并报错，而不是在内存里悄悄继续。

冷却、会话粘性、认证失败计数和配额缓存只在内存中，重启后清空。

### 备份与迁移

先停止服务（或容器），再复制整个数据目录。正常退出后日志已经合并，复制 `config.json` 和 `metadata.json` 就足够；保险起见可以连 `store.journal` 一起复制。

同一个数据目录同一时间只能被一个进程打开。第二个进程会因为拿不到锁而启动失败。

## 访问控制

### 没有设置密钥时

模型接口和管理接口分别判断。某一组接口没有任何可用的密钥时（模型接口：没有 `PROXY_KEY` 也没有签发的密钥；管理接口：没有 `ADMIN_KEY` 也没有 `PROXY_KEY`），只接受满足以下**全部**条件的请求：

1. 连接被认为来自本机，满足下列任意一条：
   - 对端是回环地址。
   - 对端是回环地址或在 `TRUSTED_PROXIES` 中、且带有 `X-Forwarded-For` / `X-Real-IP` 时，按其中记录的真实客户端判断，真实客户端也必须是回环地址。
   - 开启了 `TRUST_LOCAL_PORT_FORWARD`。
2. Host 头是 `localhost` 或回环 IP。这一条用于防御 DNS 重绑定，所以即使连接来自本机，通过域名访问也会被拒绝。

不满足时返回 403（`access_error`）。

`TRUST_LOCAL_PORT_FORWARD` 相当于告诉网关"能连到这个端口的只有本机"。Docker Compose 把端口绑定在宿主机的 `127.0.0.1:3123` 上，这个声明成立。一旦端口对外暴露，任何人只要把 Host 头写成 `localhost` 就会被当作本机访问，所以对外暴露时必须设置密钥。启动日志会针对这种组合打印警告。

### 设置了密钥以后

只要设置了任何一种密钥，上面的"仅限本机"判断就不再适用，各接口按各自的密钥认证。但某一组接口本身没有密钥时，它仍然只接受本机访问：例如只签发了代理密钥、没有设置 `ADMIN_KEY` 和 `PROXY_KEY` 时，控制台只能从本机打开。

### 来源检查

所有请求都会检查浏览器的来源，防止其他网站借用户的浏览器调用接口：

- 带 `Sec-Fetch-Site: cross-site` 的请求一律拒绝。
- 带 `Origin` 头时，`Origin` 必须与请求的 Host 和协议一致；或者设置了密钥和 `PUBLIC_BASE_URL`，且 `Origin` 与 `PUBLIC_BASE_URL` 一致。
- 不带 `Origin` 的请求（命令行工具、SDK、同源的 GET）不受影响。

放在做 HTTPS 终结的反向代理后面时，浏览器发来的 `Origin` 是 `https://…`，而网关自己收到的是 HTTP 连接，协议对不上。所以**必须把 `PUBLIC_BASE_URL` 设为对外的 HTTPS 地址**，否则控制台里的保存操作会被 403 拒绝。

### 安全响应头

每个响应都带有 `Content-Security-Policy`（只允许本站脚本和本站连接、禁止被嵌入框架）、`X-Frame-Options: DENY`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cross-Origin-Opener-Policy: same-origin` 和 `Permissions-Policy`。除静态资源外，响应都带 `Cache-Control: no-store`。

认证失败限流的规则见 [HTTP 接口 · 认证失败限流](api.md#认证失败限流)。

## 反向代理

对外提供服务时，建议让网关只监听本机，由 Nginx、Caddy 等反向代理负责 HTTPS。需要做到这几点：

1. 设置 `ADMIN_KEY` 和 `PROXY_KEY`（或只签发代理密钥给客户端）。
2. 设置 `PUBLIC_BASE_URL` 为对外的 HTTPS 地址。
3. 关闭响应缓冲，放宽读取超时，让流式输出能即时到达客户端。非流式请求最长可达 600 秒，流式请求两次事件之间最长可静默 300 秒，超时应不小于 600 秒。
4. 转发真实客户端 IP，并把反代的地址加入 `TRUSTED_PROXIES`，让认证失败限流按客户端分别计数。

Nginx 示例：

```nginx
server {
    listen 443 ssl http2;
    server_name cline.example.com;

    ssl_certificate     /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:3123;
        proxy_http_version 1.1;

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

`TRUSTED_PROXIES` 填什么取决于反代怎样连到网关：

| 部署方式 | 网关看到的来源 | `TRUSTED_PROXIES` |
|---|---|---|
| 网关直接运行在宿主机，反代也在宿主机 | `127.0.0.1` | 不需要，回环地址默认可信 |
| 网关在 Docker 中，宿主机上的反代经端口映射访问 | 容器网络的网关地址，如 `172.18.0.1` | 填这个地址 |
| 反代和网关在同一个 Docker 网络，直接访问容器 | 反代容器的 IP | 填反代容器的固定 IP |

容器网络的网关地址可以这样查：

```bash
docker inspect cline-pass-console --format '{{range .NetworkSettings.Networks}}{{.Gateway}}{{end}}'
```

只填确切的地址，不要填整段私网网段，否则同一网络里的其他容器也能伪造来源。反代前面还有 CDN（例如 Cloudflare 的代理模式）时，反代看到的是 CDN 的地址，需要先在反代上还原真实 IP（Nginx 的 `real_ip` 模块），否则所有用户仍然只会被归为少数几个 CDN 地址。

## 流共享与回放

Responses 流式请求的上游流会被记录在一个回放缓冲区里。上游流还没结束时，如果又来了一个完全相同的请求（例如客户端断线后立即重连），新请求接入同一个上游流、从头回放，而不是再请求一次上游。

"完全相同"指这些都一致：模型、原始请求体（`stream` 字段除外）、转换后的 Chat 请求体、渠道设置、使用的代理密钥和绑定的账号。不同密钥的请求不会共享。

最后一个客户端离开后，网关再等 400 毫秒；没有新客户端接入就取消上游请求。

回放缓冲区的资源上限：

| 项 | 上限 |
|---|---|
| 内存分配单位 | 32 KiB |
| 单个流的内存 | 1 MiB |
| 所有流合计的内存 | 32 MiB |
| 单个流的总回放量 | 128 MiB |
| 所有流合计的总回放量 | 1 GiB |

超过任一内存上限时，这个流的缓冲整体转存到系统临时目录的文件里（`cline-pass-replay-*`）。超过总回放量上限或磁盘出错时，网关停止读取该上游，客户端收到 `replay_storage_error`，请求历史记为失败；不会丢掉旧内容后继续报告成功。流结束且所有客户端离开后，内存和临时文件随即释放。进程异常退出时可能留下临时文件。

这些上限只针对回放缓冲区，不是进程的总内存上限。

其他与流式输出有关的限制：

- 向客户端写数据时，单次写入和刷新最多阻塞 30 秒。超时的客户端被断开，共享同一上游的其他客户端不受影响。
- 单个 SSE 事件最大 8 MiB。
- 保活心跳由每个客户端各自发送，不进入回放缓冲区。

## 启动与退出

启动时日志会打印监听地址、控制台地址和代理地址（设置了 `PUBLIC_BASE_URL` 时以它为准）。账号池里没有可用账号时会打印提示；某一组接口没有密钥、同时又开启了 `TRUST_LOCAL_PORT_FORWARD` 或配置了 `TRUSTED_PROXIES` 时，会打印警告，提醒确认端口没有对外暴露。

收到 `SIGINT` 或 `SIGTERM` 后：

1. 停止接受新连接，最多等 10 秒让进行中的请求结束。
2. 超时后强制关闭剩余连接。长时间的流式请求会在这里被中断。
3. 取消所有共享的上游流，最多再等 5 秒，让被中断的请求写完请求历史。
4. 合并操作日志，关闭数据目录。

健康检查地址是 `GET /healthz`，返回 `{"ok": true}`。镜像和 Compose 文件里都没有定义 `HEALTHCHECK`，需要时请在编排工具里自行配置。

## 升级

Docker Compose：

```bash
git pull
docker compose up -d --build
```

源码运行：拉取代码后按 [README](../README.md#源码编译运行) 重新构建，停止旧进程再启动新的。

数据目录向后兼容：旧版本配置里的字段（例如顶层的 `apiKey`、单个渠道的 `upstream`）会在启动时自动迁移。升级前备份一次数据目录更稳妥。
