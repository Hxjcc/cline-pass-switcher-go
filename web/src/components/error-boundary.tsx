import { Component, type ErrorInfo, type ReactNode } from "react"

export class ErrorBoundary extends Component<{ children: ReactNode; onError?: () => void }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Console render failed", error, info.componentStack)
    this.props.onError?.()
  }
  render() {
    if (this.state.failed) return (
      <main className="mx-auto max-w-xl space-y-4 p-8" role="alert">
        <h1 className="text-xl font-semibold">控制台暂时无法显示</h1>
        <p>页面遇到错误。请重新加载；如果问题持续，请检查服务状态。</p>
        <button className="rounded border px-4 py-2" onClick={() => window.location.reload()}>重新加载</button>
      </main>
    )
    return this.props.children
  }
}
