// Panel headings are static, so the load skeleton in App.tsx can show them
// before the first response lands. Both sides read the same strings from here
// instead of keeping two copies in sync by hand.

export const MODELS_PANEL_COPY = {
  title: "订阅模型",
  description: "管理已订阅的模型及其上游渠道偏好。客户端通过 /v1/models 获取的即为此列表。",
} as const

export const ACCOUNTS_PANEL_COPY = {
  title: "账号池",
  description:
    "管理 Cline Pass 账号与调度方式，并查看各周期的套餐用量；用量已满的账号在有其他可用账号时会被跳过。",
} as const

export const KEYS_PANEL_COPY = {
  title: "代理密钥",
  description: "为下游客户端签发独立的访问密钥，可分别限定使用的账号与累计消费上限。",
} as const

export const TEST_BENCH_COPY = {
  title: "测试台",
  description: "向指定模型发送一条最小请求，确认实际命中的上游渠道。",
} as const
