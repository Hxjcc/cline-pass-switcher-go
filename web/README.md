# Frontend

React + TypeScript + Vite console for the Go proxy.

```bash
npm ci
npm run dev
```

Development requests under `/api` and `/v1` should be proxied to the Go server. Production builds are emitted to `../internal/webassets/dist` and embedded into the Go binary.
