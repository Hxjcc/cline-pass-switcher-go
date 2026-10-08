package httpapi

import (
	"context"
	"net/http"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/store"
)

type spendHoldContextKey struct{}

func withSpendHold(ctx context.Context, hold *store.SpendReservation) context.Context {
	if hold == nil {
		return ctx
	}
	return context.WithValue(ctx, spendHoldContextKey{}, hold)
}

func spendHoldFrom(ctx context.Context) *store.SpendReservation {
	if ctx == nil {
		return nil
	}
	hold, _ := ctx.Value(spendHoldContextKey{}).(*store.SpendReservation)
	return hold
}

func (s *Server) reserveRequestSpend(ctx context.Context) (*store.SpendReservation, error) {
	key, ok := callerKeyFrom(ctx)
	if !ok || !key.Issued {
		return nil, nil
	}
	return s.store.ReserveSpend(model.ProxyKeyGrant{ID: key.ID, SpendLimitUSD: key.SpendLimitUSD})
}

// Buffered Responses and compaction-trigger requests do not share an upstream
// run. Their handler owns this deferred reservation until its record is saved.
func (s *Server) admitDeferredSpend(writer http.ResponseWriter, request *http.Request) (*store.SpendReservation, bool) {
	hold, err := s.reserveRequestSpend(request.Context())
	if err != nil {
		writeSpendError(writer, err)
		return nil, false
	}
	*request = *request.WithContext(withSpendHold(request.Context(), hold))
	return hold, true
}
