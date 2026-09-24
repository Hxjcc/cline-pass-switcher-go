import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import {
  Activity,
  Boxes,
  Check,
  Copy,
  KeyRound,
  RadioTower,
  RefreshCw,
  Router,
  ShieldCheck,
  ShieldOff,
  TestTube2,
  Users,
} from "lucide-react"
import { toast } from "sonner"

import { BrandMark } from "@/components/brand-mark"
import { AccountsPanel } from "@/components/accounts-panel"
import { HistoryPanel } from "@/components/history-panel"
import { KeysPanel } from "@/components/keys-panel"
import { LoginDialog } from "@/components/login-dialog"
import { MetricCard } from "@/components/metric-card"
import { ModelsPanel } from "@/components/models-panel"
import { SecurityPanel } from "@/components/security-panel"
import { TestBench } from "@/components/test-bench"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api, errorMessage, UnauthorizedError } from "@/lib/api"
import { readAdminKey, readPersistentAdminKey, storeAdminKey } from "@/lib/admin-key"
import { HISTORY_PAGE_SIZE, fetchSnapshot, readSnapshot, writeSnapshot, type CachedSnapshot } from "@/lib/console-snapshot"
import { runProbeBatch, type ProbeBatchResult } from "@/lib/probe-batch"
import { cn } from "@/lib/utils"
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

const TAB_STORAGE = "cline-pass-switcher-tab"

const TABS = [
  { value: "overview", label: "模型与上游", icon: Boxes },
  { value: "accounts", label: "账号池", icon: Users },
  { value: "keys", label: "代理密钥", icon: KeyRound },
  { value: "security", label: "访问与安全", icon: ShieldCheck },
  { value: "test", label: "测试台", icon: TestTube2 },
  { value: "history", label: "请求历史", icon: Activity },
] as const

// The session may remember a tab this build no longer has.
function initialTab() {
  const stored = sessionStorage.getItem(TAB_STORAGE)
  return TABS.some((item) => item.value === stored) ? stored! : "overview"
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
  const [tab, setTab] = useState(initialTab)
  const [refreshing, setRefreshing] = useState(false)
  const [proxyBaseCopied, setProxyBaseCopied] = useState(false)
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
    const load = async () => {
      // With no credential stored, ask the public /api/meta first: firing the
      // five protected calls of a fresh page load counts as failed attempts on
      // the server and locked the operator out of his own console for a while.
      if (!authKey) {
        const probe = await api<MetaResponse>("/api/meta")
        if (probe.authRequired) return
      }
      const snapshot = await fetchSnapshot(authKey)
      if (active) applySnapshot(snapshot)
    }
    void load().catch((error: unknown) => { if (active) handleError(error) })
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
    writeSnapshot({ models, meta, history, accounts, keys, security })
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
    value: Pick<SecurityResponse, "proxyKey" | "adminKey" | "publicBaseUrl">,
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

  const copyProxyBase = async () => {
    try {
      await navigator.clipboard.writeText(proxyBase)
      setProxyBaseCopied(true)
      toast.success("代理地址已复制")
      window.setTimeout(() => setProxyBaseCopied(false), 1600)
    } catch {
      toast.error("无法访问剪贴板")
    }
  }

  return (
    <div data-app-shell className="min-h-svh">
      <header className="bg-card sticky top-0 z-40 border-b">
        <div className="mx-auto flex max-w-[1400px] flex-wrap items-center gap-x-4 gap-y-3 px-4 py-3">
          <div className="flex min-w-0 flex-1 items-center gap-3">
            <BrandMark className="size-9 shrink-0" />
            <div className="min-w-0 space-y-1">
              <h1 className="truncate text-base leading-5 font-semibold">Cline Pass 上游控制台</h1>
              <div className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 text-xs leading-6">
                {meta && (
                  <>
                    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                      <span
                        aria-hidden="true"
                        className={cn(
                          "size-1.5 rounded-full",
                          meta.configured ? "bg-emerald-500" : "bg-destructive",
                        )}
                      />
                      <span className={cn(!meta.configured && "text-destructive")}>
                        {meta.configured ? "账号已就绪" : "尚未配置账号"}
                      </span>
                    </span>
                    <span aria-hidden="true" className="bg-border h-3 w-px" />
                    <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                      {meta.authRequired ? (
                        <ShieldCheck className="size-3.5" aria-hidden="true" />
                      ) : (
                        <ShieldOff className="size-3.5" aria-hidden="true" />
                      )}
                      {meta.authRequired ? "已启用鉴权" : "未启用鉴权"}
                    </span>
                    <span aria-hidden="true" className="bg-border h-3 w-px" />
                  </>
                )}
                <span className="inline-flex min-w-0 items-center gap-1.5">
                  <Router className="size-3.5 shrink-0" aria-hidden="true" />
                  <span className="truncate font-mono" title="客户端使用的代理地址">
                    {proxyBase}
                  </span>
                  <Tooltip>
                    <TooltipTrigger
                      render={
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          className="text-muted-foreground -my-1"
                          aria-label="复制代理地址"
                          onClick={() => void copyProxyBase()}
                        />
                      }
                    >
                      {proxyBaseCopied ? <Check /> : <Copy />}
                    </TooltipTrigger>
                    <TooltipContent>复制代理地址</TooltipContent>
                  </Tooltip>
                </span>
              </div>
            </div>
          </div>
          <div className="ml-auto flex items-center gap-2">
            {meta?.authRequired && (
              <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}>
                <KeyRound data-icon="inline-start" />
                切换管理密钥
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
            <AlertDescription>请在「账号池」中添加账号及 API Key，保存后即可使用代理。</AlertDescription>
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
                detail="客户端可见的模型"
                icon={Boxes}
              />
              <MetricCard
                label="启用账号"
                value={accounts ? statistics.enabledAccounts : "—"}
                detail={accounts?.mode === "roundrobin" ? "调度模式：账号池轮询" : "调度模式：单账号"}
                icon={Users}
              />
              <MetricCard
                label="累计请求"
                value={accounts ? statistics.requestCount : "—"}
                detail="所有账号合计"
                icon={Activity}
              />
              <MetricCard
                label="已观测渠道"
                value={models ? statistics.observedProviders : "—"}
                detail="来自请求记录与探测结果"
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
                proxyBase={proxyBase}
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
        </Tabs>
      </main>

      <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} onLogin={login} />
    </div>
  )
}

export default App
