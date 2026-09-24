package httpapi

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

// spendHolds counts the running requests of every spend-limited key. The
// reported spend only moves once a request has finished, so without this a
// burst of concurrent requests would all pass the limit check together.
type spendHolds struct {
	mu      sync.Mutex
	running map[string]int
}

func newSpendHolds() *spendHolds {
	return &spendHolds{running: map[string]int{}}
}

// spendHold is one admitted request. The handler owns the first reference; a
// shared upstream run that outlives the handler retains another, so the slot
// is only freed once the spend has been recorded.
type spendHold struct {
	holds *spendHolds
	keyID string
	refs  atomic.Int32
}

func (hold *spendHold) retain() {
	if hold != nil {
		hold.refs.Add(1)
	}
}

func (hold *spendHold) release() {
	if hold == nil || hold.refs.Add(-1) != 0 {
		return
	}
	hold.holds.mu.Lock()
	defer hold.holds.mu.Unlock()
	hold.holds.running[hold.keyID]--
	if hold.holds.running[hold.keyID] <= 0 {
		delete(hold.holds.running, hold.keyID)
	}
}

// admit reserves a slot for one request of a spend-limited key, or explains
// why it may not start. The running requests are each expected to cost the
// key's average so far; the estimate only gates admission and is never
// charged.
func (holds *spendHolds) admit(grant model.ProxyKeyGrant, usage model.KeyUsage) (*spendHold, string) {
	if grant.SpendLimitUSD <= 0 {
		return nil, ""
	}
	holds.mu.Lock()
	defer holds.mu.Unlock()
	running := holds.running[grant.ID]
	if running > 0 && usage.Requests > 0 {
		expected := usage.SpentMicroUSD / usage.Requests * int64(running)
		if float64(usage.SpentMicroUSD+expected)/1e6 >= grant.SpendLimitUSD {
			return nil, "该代理密钥的额度即将用尽（限额 " + formatUSD(grant.SpendLimitUSD) +
				"，已用 " + formatUSD(usage.SpentUSD()) + "，另有 " + strconv.Itoa(running) +
				" 个请求进行中，预计还会花费约 " + formatUSD(float64(expected)/1e6) + "），请等这些请求结束后再试"
		}
	}
	holds.running[grant.ID] = running + 1
	hold := &spendHold{holds: holds, keyID: grant.ID}
	hold.refs.Store(1)
	return hold, ""
}

type spendHoldContextKey struct{}

func withSpendHold(ctx context.Context, hold *spendHold) context.Context {
	if hold == nil {
		return ctx
	}
	return context.WithValue(ctx, spendHoldContextKey{}, hold)
}

func spendHoldFrom(ctx context.Context) *spendHold {
	if ctx == nil {
		return nil
	}
	hold, _ := ctx.Value(spendHoldContextKey{}).(*spendHold)
	return hold
}
