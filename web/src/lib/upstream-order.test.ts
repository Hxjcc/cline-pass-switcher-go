import { expect, test } from "vitest"
import { orderedUpstreams } from "./upstream-order"
import type { ModelConfig, ModelMeta, SubscriptionModel } from "@/types"

function model(config: Partial<ModelConfig>, meta: ModelMeta | null): SubscriptionModel {
  return {
    id: "cline-pass/test",
    config: {
      upstreams: [],
      exclude: [],
      pinMode: "strict",
      ...config,
    },
    meta,
  }
}

test("keeps the pinned order, then sorts the rest by health", () => {
  const ordered = orderedUpstreams(
    model(
      { upstreams: ["pinned-b", "pinned-a"] },
      {
        upstreams: ["broken", "pinned-a", "pinned-b", "healthy"],
        upstreamStatus: {
          broken: { status: "bad", note: "", checkedAt: 1 },
          healthy: { status: "ok", note: "", checkedAt: 1 },
        },
      },
    ),
  )
  // Pinned channels keep the operator's order, never the health order.
  expect(ordered.slice(0, 2)).toEqual(["pinned-b", "pinned-a"])
  // The rest are ranked: healthy before unknown before broken.
  expect(ordered.slice(2)).toEqual(["healthy", "broken"])
})

test("collects channels from every source, without duplicates", () => {
  const ordered = orderedUpstreams(
    model(
      { upstreams: ["a"], exclude: ["b"] },
      {
        upstreams: ["a", "b", "c"],
        upstreamDetail: { c: { slug: "c", name: "C", endpoints: 1, context: 1, uptime: 1 }, d: { slug: "d", name: "D", endpoints: 1, context: 1, uptime: 1 } },
      },
    ),
  )
  expect([...ordered].sort()).toEqual(["a", "b", "c", "d"])
})

// A pin the last probe did not report is still listed: the row has to show what
// the operator configured, otherwise the stale pin is invisible and cannot be
// removed from the UI.
test("keeps a pinned channel the probe has not seen", () => {
  const ordered = orderedUpstreams(model({ upstreams: ["stale", "kept"] }, { upstreams: ["kept"] }))
  expect(ordered).toEqual(["stale", "kept"])
})

test("survives a model that was never probed", () => {
  expect(orderedUpstreams(model({}, null))).toEqual([])
})
