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
  if (options.signal) {
    init.signal = options.signal
  }
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
  return payload as T
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}
