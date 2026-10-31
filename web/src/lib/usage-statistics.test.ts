// @vitest-environment node

import { expect, test } from "vitest"
import { formatUsageTokens, usageDate, usagePath, validateUsageFilter } from "./usage-statistics"

test("calendar presets use UTC+8 independently of the browser timezone", () => {
  expect(usageDate(0, Date.parse("2026-10-23T15:59:59Z"))).toBe("2026-10-23")
  expect(usageDate(0, Date.parse("2026-10-23T16:00:00Z"))).toBe("2026-10-24")
  expect(usageDate(-1, Date.parse("2026-10-23T16:00:00Z"))).toBe("2026-10-23")
})

test("filters encode identities and reject reversed or oversized ranges", () => {
  const q = { from: "2026-10-01", to: "2026-10-24", model: "cline-pass/alpha", account: "a", key: "k" }
  expect(usagePath(q)).toContain("model=cline-pass%2Falpha&account=a&key=k")
  expect(validateUsageFilter(q)).toBe("")
  expect(validateUsageFilter({ ...q, from: "2026-10-25" })).toContain("开始日期")
  expect(validateUsageFilter({ ...q, from: "2020-01-01" })).toContain("365")
})

test("large statistical totals use readable M and B units", () => {
  expect(formatUsageTokens(25600000)).toBe("25.6M")
  expect(formatUsageTokens(1200000000)).toBe("1.2B")
  expect(formatUsageTokens(1234)).toBe("1,234")
})
