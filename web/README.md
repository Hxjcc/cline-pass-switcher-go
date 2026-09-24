# 控制台前端

网关内嵌的管理控制台，使用 React、TypeScript、Vite 和 Tailwind CSS。

```bash
npm ci
npm run dev
```

开发服务器把 `/api` 和 `/v1` 转发到 `http://127.0.0.1:3123`，需要先在本机启动网关。`npm run build` 的产物输出到 `../internal/webassets/dist`，由 Go 嵌入二进制。

构建、测试和 CI 的说明见 [开发说明](../docs/development.md)。
