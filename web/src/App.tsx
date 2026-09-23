import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  Activity,
  Boxes,
  KeyRound,
  RadioTower,
  RefreshCw,
  Router,
  Settings2,
  ShieldCheck,
  TestTube2,
  Users,
} from "lucide-react"
import { toast } from "sonner"

import { BrandMark } from "@/components/brand-mark"
import { AccountsPanel } from "@/components/accounts-panel"
import { CatalogPanel } from "@/components/catalog-panel"
import { HistoryPanel } from "@/components/history-panel"
import { KeysPanel } from "@/components/keys-panel"
import { LoginDialog } from "@/components/login-dialog"
import { MetricCard } from "@/components/metric-card"
import { ModelsPanel } from "@/components/models-panel"
import { SecurityPanel } from "@/components/security-panel"
import { TestBench } from "@/components/test-bench"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api, errorMessage, UnauthorizedError } from "@/lib/api"
import { runProbeBatch, type ProbeBatchResult } from "@/lib/probe-batch"
import type {
  AccountTestResponse,
  AccountsResponse,
  HistoryResponse,
  KeysResponse,
  MetaResponse,
  ModelConfig,
  ModelsResponse,
  OfficialResponse,
  ProbeResponse,
  ProxyKeyDraft,
  QuotaResponse,
  SecurityResponse,
  TestResponse,
  ValidationResponse,
} from "@/types"

const ADMIN_KEY_STORAGE = "cline-pass-switcher-admin-key"
const SNAPSHOT_STORAGE = "cline-pass-switcher-snapshot"
const TAB_STORAGE = "cline-pass-switcher-tab"
const HISTORY_PAGE_SIZE = 50

// The admin key is the only credential for the console and the management API.
// The client keys issued below never open this page. The credential is kept in
// sessionStorage by default (gone when the tab closes) and only written to
// localStorage when the operator ticks "remember this device" at login.
function readPersistentAdminKey(): string {
  try {
    return localStorage.getItem(ADMIN_KEY_STORAGE) ?? ""
  } catch {
    return ""
  }
}

function readAdminKey(): string {
  try {
    return sessionStorage.getItem(ADMIN_KEY_STORAGE) ?? readPersistentAdminKey()
  } catch {
    return readPersistentAdminKey()
  }
}

function storeAdminKey(key: string, remember: boolean) {
  try {
    if (key) sessionStorage.setItem(ADMIN_KEY_STORAGE, key)
    else sessionStorage.removeItem(ADMIN_KEY_STORAGE)
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
  try {
    if (key && remember) localStorage.setItem(ADMIN_KEY_STORAGE, key)
    else localStorage.removeItem(ADMIN_KEY_STORAGE)
  } catch {
    // Storage can be unavailable in restricted browser contexts.
  }
}

const TABS = [
  { value: "overview", label: "模型与上游", icon: Boxes },
  { value: "accounts", label: "账号池", icon: Users },
  { value: "keys", label: "代理密钥", icon: KeyRound },
  { value: "security", label: "访问与安全", icon: ShieldCheck },
  { value: "test", label: "测试台", icon: TestTube2 },
  { value: "history", label: "请求历史", icon: Activity },
  { value: "catalog", label: "完整目录", icon: Settings2 },
] as const

interface CachedSnapshot {
  models: ModelsResponse
  meta: MetaResponse
  history: HistoryResponse["history"]
  historyTotal?: number
  historyHasMore?: boolean
  accounts: AccountsResponse
  keys: KeysResponse
  security: SecurityResponse
}

async function fetchSnapshot(key: string): Promise<CachedSnapshot> {
  const [meta, models, accounts, keys, security, history] = await Promise.all([
    api<MetaResponse>("/api/meta"),
    api<ModelsResponse>("/api/models", { key }),
    api<AccountsResponse>("/api/accounts", { key }),
    api<KeysResponse>("/api/keys", { key }),
    api<SecurityResponse>("/api/security", { key }),
    api<HistoryResponse>(`/api/history?limit=${HISTORY_PAGE_SIZE}`, { key }),
  ])
  return {
    meta,
    models,
    accounts,
    keys,
    security,
    history: history.history,
    historyTotal: history.total,
    historyHasMore: history.hasMore,
  }
}

function readSnapshot(): CachedSnapshot | null {
  try {
    const raw = sessionStorage.getItem(SNAPSHOT_STORAGE)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<CachedSnapshot>
    if (
      !parsed.models?.subscription ||
      !parsed.meta ||
      !Array.isArray(parsed.history) ||
      !parsed.accounts ||
      !parsed.keys ||
      !parsed.security
    ) {
      return null
    }
    return parsed as CachedSnapshot
  } catch {
    return null
  }
}

function App() {
  const [initialSnapshot] = useState(readSnapshot)
  const [authKey, setAuthKey] = useState(readAdminKey)
  const [meta, setMeta] = useState<MetaResponse | null>(initialSnapshot?.meta ?? null)
  const [models, setModels] = useState<ModelsResponse | null>(initialSnapshot?.models ?? null)
  const [accounts, setAccounts] = useState<AccountsResponse | null>(
    initialSnapshot?.accounts ?? null,
  )
  const [keys, setKeys] = useState<KeysResponse | null>(initialSnapshot?.keys ?? null)
  const [security, setSecurity] = useState<SecurityResponse | null>(
    initialSnapshot?.security ?? null,
  )
  const [history, setHistory] = useState<HistoryResponse["history"]>(
    initialSnapshot?.history ?? [],
  )
  const [historyTotal, setHistoryTotal] = useState(
    initialSnapshot?.historyTotal ?? initialSnapshot?.history.length ?? 0,
  )
  const [historyHasMore, setHistoryHasMore] = useState(initialSnapshot?.historyHasMore ?? false)
  const [historyQuery, setHistoryQuery] = useState<{ q: string; onlyErrors: boolean }>({
    q: "",
    onlyErrors: false,
  })
  const [loginOpen, setLoginOpen] = useState(false)
  const [tab, setTab] = useState(() => sessionStorage.getItem(TAB_STORAGE) || "overview")
  const [refreshing, setRefreshing] = useState(false)
  const [batchProbe, setBatchProbe] = useState<{ done: number; total: number } | null>(null)
  const batchProbeAbort = useRef<AbortController | null>(null)

  const handleError = useCallback((error: unknown) => {
    if (error instanceof UnauthorizedError) {
      setLoginOpen(true)
      return
    }
    toast.error(errorMessage(error))
  }, [])

  const loadModels = async (key = authKey) => {
    const response = await api<ModelsResponse>("/api/models", { key })
    setModels(response)
    return response
  }

  // The log is paged: the panel asks for the next slice with the filter it is
  // currently showing, and appended pages keep the newest-first order.
  const loadHistory = useCallback(
    async (
      options: { offset?: number; limit?: number; q?: string; onlyErrors?: boolean } = {},
    ) => {
      const offset = options.offset ?? 0
      const limit = Math.min(Math.max(options.limit ?? HISTORY_PAGE_SIZE, 1), 200)
      const params = new URLSearchParams({
        limit: String(limit),
        offset: String(offset),
      })
      if (options.q?.trim()) params.set("q", options.q.trim())
      if (options.onlyErrors) params.set("result", "error")
      const response = await api<HistoryResponse>(`/api/history?${params.toString()}`, {
        key: authKey,
      })
      setHistory((current) => (offset > 0 ? [...current, ...response.history] : response.history))
      setHistoryTotal(response.total)
      setHistoryHasMore(response.hasMore)
      return response
    },
    [authKey],
  )

  const applyHistoryQuery = useCallback(
    (next: { q: string; onlyErrors: boolean }) => {
      setHistoryQuery(next)
      void loadHistory({ q: next.q, onlyErrors: next.onlyErrors }).catch(handleError)
    },
    [loadHistory, handleError],
  )

  const refreshHistory = useCallback(
    () =>
      loadHistory({
        // Refreshing keeps the window the operator has scrolled to instead of
        // collapsing a loaded second page back to the first one.
        limit: Math.max(HISTORY_PAGE_SIZE, Math.min(history.length, 200)),
        q: historyQuery.q,
        onlyErrors: historyQuery.onlyErrors,
      }),
    [loadHistory, history.length, historyQuery],
  )

  const loadMoreHistory = useCallback(
    () =>
      loadHistory({
        offset: history.length,
        q: historyQuery.q,
        onlyErrors: historyQuery.onlyErrors,
      }),
    [loadHistory, history.length, historyQuery],
  )

  const applySnapshot = useCallback((snapshot: CachedSnapshot) => {
    setMeta(snapshot.meta)
    setModels(snapshot.models)
    setAccounts(snapshot.accounts)
    setKeys(snapshot.keys)
    setSecurity(snapshot.security)
    setHistory(snapshot.history)
    setHistoryTotal(snapshot.historyTotal ?? snapshot.history.length)
    setHistoryHasMore(snapshot.historyHasMore ?? false)
  }, [])

  const loadAll = async (key = authKey) => {
    applySnapshot(await fetchSnapshot(key))
  }

  useEffect(() => {
    let active = true
    void fetchSnapshot(authKey)
      .then((snapshot) => { if (active) applySnapshot(snapshot) })
      .catch((error: unknown) => { if (active) handleError(error) })
    return () => { active = false }
  }, [authKey, applySnapshot, handleError])

  useEffect(() => {
    let active = true
    void api<MetaResponse>("/api/meta")
      .then((response) => {
        if (!active) return
        setMeta(response)
        if (response.authRequired && !authKey) {
          setLoginOpen(true)
        }
      })
      .catch((error: unknown) => { if (active) handleError(error) })
    return () => { active = false }
  }, [authKey, handleError])

  useEffect(() => {
    if (!models || !meta || !accounts || !keys || !security) return
    try {
      sessionStorage.setItem(
        SNAPSHOT_STORAGE,
        JSON.stringify({ models, meta, history, accounts, keys, security } satisfies CachedSnapshot),
      )
    } catch {
      // Storage can be unavailable in restricted browser contexts.
    }
  }, [accounts, history, keys, meta, models, security])

  useEffect(() => {
    sessionStorage.setItem(TAB_STORAGE, tab)
  }, [tab])

  const login = async (key: string, remember = false) => {
    await api<ModelsResponse>("/api/models", { key })
    storeAdminKey(key, remember)
    setAuthKey(key)
  }

  const refresh = async () => {
    setRefreshing(true)
    try {
      await loadAll()
      toast.success("数据已刷新")
    } catch (error) {
      handleError(error)
    } finally {
      setRefreshing(false)
    }
  }

  const saveAccounts = async (value: AccountsResponse) => {
    await api("/api/accounts", {
      key: authKey,
      body: {
        accounts: value.accounts,
        mode: value.mode,
        active: value.active,
      },
    })
    const response = await api<AccountsResponse>("/api/accounts", { key: authKey })
    setAccounts(response)
  }

  const testAccount = (key: string, id?: string) =>
    api<AccountTestResponse>("/api/accounts/test", {
      key: authKey,
      body: { key, id },
    })

  // Issued client keys: the console only ever sends the list; the server owns
  // identities, spend counters and the spend limit verdict.
  const saveKeys = async (value: ProxyKeyDraft[]) => {
    const response = await api<KeysResponse>("/api/keys", {
      key: authKey,
      body: {
        keys: value.map((row) => ({
          id: row.id.startsWith("draft_") ? "" : row.id,
          name: row.name,
          key: row.key,
          enabled: row.enabled,
          accountId: row.accountId,
          spendLimitUsd: row.spendLimitUsd,
          note: row.note,
          createdAt: row.createdAt,
        })),
      },
    })
    setKeys(response)
    return response
  }

  const revealKeys = () => api<KeysResponse>("/api/keys?reveal=1", { key: authKey })

  const resetKeyUsage = async (id: string, all = false) => {
    const response = await api<KeysResponse>("/api/keys/reset", {
      key: authKey,
      body: { id, all },
    })
    setKeys(response)
    return response
  }

  // Stored keys stay hidden until the user asks for them; the reveal response
  // is kept in the panel and never written to the cached snapshot.
  const revealAccounts = () => api<AccountsResponse>("/api/accounts?reveal=1", { key: authKey })

  // The panel asks for plan utilization. The server also reads it while
  // choosing an account, and skips one whose window is already at 100%.
  const loadQuota = useCallback((refresh = false) =>
    api<QuotaResponse>(refresh ? "/api/accounts/quota?refresh=1" : "/api/accounts/quota", {
      key: authKey,
    }), [authKey])

  const saveSecurity = async (
    value: Pick<SecurityResponse, "proxyKey" | "publicBaseUrl" | "exposeCatalog">,
  ) => {
    const response = await api<SecurityResponse>("/api/security", {
      key: authKey,
      body: value,
    })
    // Once an admin key exists the console answers to it, so the stored
    // credential has to follow - otherwise the next reload would be locked
    // out with the client key.
    const nextKey = response.adminKey || response.proxyKey || ""
    // Changing the key keeps whatever lifetime the operator already chose.
    storeAdminKey(nextKey, readPersistentAdminKey() !== "")
    setAuthKey(nextKey)
    setSecurity(response)
    if (response.proxyBase) {
      setModels((current) => (current ? { ...current, proxyBase: response.proxyBase! } : current))
    }
    return response
  }

  const probe = async (modelID: string) => {
    const response = await api<ProbeResponse>("/api/probe", {
      key: authKey,
      body: { model: modelID },
    })
    await loadModels()
    return response
  }

  // Probing every model is one real upstream request each, so the batch runs a
  // few at a time, reports progress, and can be stopped without leaving the
  // panel stuck on "probing".
  const probeAll = async () => {
    const list = models?.subscription ?? []
    if (!list.length || batchProbeAbort.current) {
      return { ok: 0, failed: 0, aborted: false }
    }
    const controller = new AbortController()
    batchProbeAbort.current = controller
    let result: ProbeBatchResult = { ok: 0, failed: 0, aborted: false }
    try {
      result = await runProbeBatch(
        list.map((model) => model.id),
        async (modelID, signal) => {
          await api<ProbeResponse>("/api/probe", {
            key: authKey,
            body: { model: modelID },
            signal,
          })
        },
        {
          signal: controller.signal,
          onProgress: (done, total) => setBatchProbe({ done, total }),
        },
      )
    } finally {
      batchProbeAbort.current = null
      setBatchProbe(null)
    }
    if (!result.aborted) {
      try {
        await loadModels()
      } catch (error) {
        handleError(error)
      }
    }
    return result
  }

  const cancelProbeAll = () => batchProbeAbort.current?.abort()

  const validate = async (modelID: string) => {
    const response = await api<ValidationResponse>("/api/validate-upstreams", {
      key: authKey,
      body: { model: modelID },
    })
    await loadModels()
    return response
  }

  const testModel = (modelID: string, upstreams: string[], exclude: string[]) =>
    api<TestResponse>("/api/test", {
      key: authKey,
      body: { model: modelID, upstreams, exclude },
    })

  const updateModelConfig = async (modelID: string, config: ModelConfig) => {
    const previous = models?.subscription.find((model) => model.id === modelID)?.config
    setModels((current) =>
      current
        ? {
            ...current,
            subscription: current.subscription.map((model) =>
              model.id === modelID ? { ...model, config } : model,
            ),
          }
        : current,
    )
    try {
      await api("/api/config", {
        key: authKey,
        body: { perModel: { [modelID]: config } },
      })
    } catch (error) {
      if (previous) {
        setModels((current) =>
          current
            ? {
                ...current,
                subscription: current.subscription.map((model) =>
                  model.id === modelID ? { ...model, config: previous } : model,
                ),
              }
            : current,
        )
      }
      throw error
    }
  }

  const fetchOfficial = async () => {
    const response = await api<OfficialResponse>("/api/fetch-official-models", {
      key: authKey,
      body: {},
    })
    await loadModels()
    return response
  }

  const removeModel = async (modelID: string) => {
    await api("/api/models/remove", { key: authKey, body: { model: modelID } })
    await loadModels()
  }

  const clearHistory = async () => {
    await api("/api/history/clear", { key: authKey, body: {} })
    setHistory([])
    setHistoryTotal(0)
    setHistoryHasMore(false)
  }

  const statistics = useMemo(() => {
    const enabledAccounts = accounts?.accounts.filter((account) => account.enabled).length ?? 0
    const requestCount = Object.values(accounts?.stats ?? {}).reduce(
      (total, item) => total + item.requests,
      0,
    )
    const observedProviders = new Set(
      models?.subscription
        .map((model) => model.meta?.lastProvider)
        .filter((value): value is string => Boolean(value)) ?? [],
    ).size
    return {
      enabledAccounts,
      requestCount,
      observedProviders,
    }
  }, [accounts, models])

  const proxyBase = models?.proxyBase ?? meta?.proxyBase ?? "http://127.0.0.1:3123/v1"

  return (
    <div data-app-shell className="min-h-svh">
      <header className="bg-card sticky top-0 z-40 border-b">
        <div className="mx-auto flex max-w-[1400px] flex-wrap items-center gap-3 px-4 py-3">
          <BrandMark className="size-10 shrink-0" />
          <div className="min-w-0 flex-1">
            <div className="flex h-6 items-center gap-2">
              <h1 className="truncate text-base leading-6 font-semibold">
                Cline Pass 上游控制台
              </h1>
              {meta && (
                <Badge variant={meta.configured ? "default" : "destructive"}>
                  {meta.configured ? "已配置" : "缺少账号"}
                </Badge>
              )}
            </div>
            <div className="text-muted-foreground flex h-4 items-center gap-2 text-xs leading-4">
              <Router className="size-3.5" />
              <span className="truncate font-mono">{proxyBase}</span>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {meta?.authRequired && (
              <Badge variant="outline">
                <ShieldCheck data-icon="inline-start" />
                鉴权开启
              </Badge>
            )}
            {meta?.authRequired && (
              <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}>
                <KeyRound data-icon="inline-start" />
                切换密钥
              </Button>
            )}
            <Button variant="outline" size="sm" onClick={refresh} disabled={refreshing}>
              <RefreshCw
                className={refreshing ? "animate-spin" : ""}
                data-icon="inline-start"
              />
              刷新
            </Button>
            <ThemeToggle />
          </div>
        </div>
      </header>

      <main className="mx-auto max-w-[1400px] space-y-4 px-4 py-5">
        {meta && !meta.configured && (
          <Alert variant="destructive">
            <KeyRound />
            <AlertTitle>尚未配置 Cline Pass 账号</AlertTitle>
            <AlertDescription>前往账号池添加账号与 API Key，保存后即可使用代理。</AlertDescription>
          </Alert>
        )}

        <Tabs value={tab} onValueChange={setTab}>
          <div className="overflow-x-auto pb-1">
            <TabsList className="w-max gap-0.5">
              {TABS.map(({ value, label, icon: Icon }) => (
                <TabsTrigger key={value} value={value} className="px-3">
                  <Icon data-icon="inline-start" />
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          <TabsContent value="overview" className="space-y-4">
            <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <MetricCard
                label="订阅模型"
                value={models ? models.subscription.length : "—"}
                detail={`${models?.catalogCount ?? 0} 个目录模型`}
                icon={Boxes}
              />
              <MetricCard
                label="启用账号"
                value={accounts ? statistics.enabledAccounts : "—"}
                detail={accounts?.mode === "roundrobin" ? "轮询模式" : "单账号模式"}
                icon={Users}
              />
              <MetricCard
                label="累计请求"
                value={accounts ? statistics.requestCount : "—"}
                detail="按账号统计"
                icon={Activity}
              />
              <MetricCard
                label="已观测渠道"
                value={models ? statistics.observedProviders : "—"}
                detail="来自请求与探测结果"
                icon={RadioTower}
              />
            </div>
            {models && (
              <ModelsPanel
                data={models}
                onRefresh={async () => {
                  await loadModels()
                }}
                onProbe={probe}
                onProbeAll={probeAll}
                probeAllProgress={batchProbe}
                onCancelProbeAll={cancelProbeAll}
                onValidate={validate}
                onTest={testModel}
                onUpdateConfig={updateModelConfig}
                onFetchOfficial={fetchOfficial}
                onRemove={removeModel}
              />
            )}
          </TabsContent>

          <TabsContent value="accounts">
            {accounts && (
              <AccountsPanel
                data={accounts}
                onSave={saveAccounts}
                onTest={testAccount}
                onReveal={revealAccounts}
                onQuota={loadQuota}
              />
            )}
          </TabsContent>

          <TabsContent value="security">
            {security && (
              <SecurityPanel data={security} proxyBase={proxyBase} onSave={saveSecurity} />
            )}
          </TabsContent>

          <TabsContent value="keys">
            {keys && accounts && (
              <KeysPanel
                data={keys}
                accounts={accounts}
                onSave={saveKeys}
                onReveal={revealKeys}
                onReset={resetKeyUsage}
              />
            )}
          </TabsContent>

          <TabsContent value="test">
            {models && <TestBench models={models.subscription} onTest={testModel} />}
          </TabsContent>

          <TabsContent value="history">
            <HistoryPanel
              history={history}
              total={historyTotal}
              hasMore={historyHasMore}
              query={historyQuery}
              onQueryChange={applyHistoryQuery}
              onRefresh={async () => {
                try {
                  await refreshHistory()
                } catch (error) {
                  handleError(error)
                }
              }}
              onLoadMore={async () => {
                try {
                  await loadMoreHistory()
                } catch (error) {
                  handleError(error)
                }
              }}
              onClear={clearHistory}
            />
          </TabsContent>

          <TabsContent value="catalog">
            {models && <CatalogPanel data={models} onProbe={probe} />}
          </TabsContent>
        </Tabs>
      </main>

      <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} onLogin={login} />
    </div>
  )
}

export default App
