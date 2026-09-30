package httpapi

import (
	"context"

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
