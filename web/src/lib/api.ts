export class UnauthorizedError extends Error {
  constructor() {
    super("unauthorized")
    this.name = "UnauthorizedError"
  }
}

export class APIError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = "APIError"
    this.status = status
    this.code = code
  }
}

export async function api<T>(
  path: string,
  options: {
    key?: string
    method?: "GET" | "POST"
    body?: unknown
    /** Cancels the request, for example when a batch run is stopped. */
    signal?: AbortSignal
    timeoutMs?: number
  } = {},
): Promise<T> {
  const headers: Record<string, string> = {}
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json"
  }
  if (options.key) {
    headers["X-Admin-Key"] = options.key
  }
  const init: RequestInit = {
    method: options.method ?? (options.body === undefined ? "GET" : "POST"),
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  }
  const controller = new AbortController()
  const abort = () => controller.abort(options.signal?.reason)
  if (options.signal?.aborted) abort()
  else options.signal?.addEventListener("abort", abort, { once: true })
  const slow = /\/api\/(probe|test|validate|accounts\/test|accounts\/quota|official|fetch)/.test(path)
  const routing = /\/api\/(verify-routing|gateway-capabilities)/.test(path)
  const timeoutMs = options.timeoutMs ?? (routing ? 600_000 : slow ? 180_000 : options.body === undefined ? 15_000 : 30_000)
  let timedOut = false
  const timer = setTimeout(() => { timedOut = true; controller.abort() }, timeoutMs)
  init.signal = controller.signal
  try {
    const response = await fetch(path, init)
    if (response.status === 401) {
      throw new UnauthorizedError()
    }
    const payload = (await response.json().catch(() => null)) as
      | (T & { error?: { message?: string; code?: string } | string })
      | null
    if (!response.ok) {
      const message =
        typeof payload?.error === "string"
          ? payload.error
          : payload?.error?.message || `请求失败（${response.status}）`
      throw new APIError(message, response.status, typeof payload?.error === "object" ? payload.error?.code : undefined)
    }
    const contentType = response.headers.get("Content-Type")?.split(";", 1)[0]?.trim().toLowerCase()
    if (!contentType || !(contentType === "application/json" || contentType.endsWith("+json")) || payload === null || typeof payload !== "object" || Array.isArray(payload)) {
      throw new APIError("服务器返回了无效的 JSON 响应，请刷新后重试", response.status, "invalid_json_response")
    }
    return payload as T
    } catch (error) {
    if (timedOut) throw new APIError("请求超时，请重试", 0, "request_timeout")
    throw error
  } finally {
    clearTimeout(timer)
    options.signal?.removeEventListener("abort", abort)
  }
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
