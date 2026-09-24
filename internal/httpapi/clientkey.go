package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/upstream"
)

// formatUSD renders an amount the way the console does, so an operator
// comparing the error message with the table sees the same number.
func formatUSD(value float64) string {
	return "$" + strconv.FormatFloat(value, 'f', -1, 64)
}

// callerKey is the credential a client request was admitted with. It travels
// in the request context so every handler can attribute the request to the key
// that caused the spend without threading the grant through each signature.
type callerKey struct {
	ID        string
	Name      string
	AccountID string
	// Issued is true when the call came from a console-issued key rather than
	// the master proxy key. Only issued keys accumulate a spend limit.
	Issued bool
}

type callerKeyContextKey struct{}

func withCallerKey(ctx context.Context, key callerKey) context.Context {
	if key.ID == "" && !key.Issued {
		return ctx
	}
	return context.WithValue(ctx, callerKeyContextKey{}, key)
}

func callerKeyFrom(ctx context.Context) (callerKey, bool) {
	if ctx == nil {
		return callerKey{}, false
	}
	key, ok := ctx.Value(callerKeyContextKey{}).(callerKey)
	return key, ok
}

// stampCallerKey copies the admitting key onto a history entry. Every record
// site calls this so per-key spend survives restarts through the journal.
func stampCallerKey(ctx context.Context, entry model.HistoryEntry) model.HistoryEntry {
	key, ok := callerKeyFrom(ctx)
	if !ok {
		return entry
	}
	entry.KeyID, entry.KeyName = key.ID, key.Name
	return entry
}

// bearerToken extracts the credential from either header style the clients and
// the console use.
func bearerToken(request *http.Request) string {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(header) >= 7 && strings.EqualFold(header[:7], "Bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func constantTimeEqual(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

// isClientPath reports the OpenAI-compatible surface a downstream client talks
// to. It is checked before the console prefix because /api/v1/* exists as a
// client-facing alias of /v1/*.
func isClientPath(path string) bool {
	return strings.HasPrefix(path, "/v1/") ||
		isModelsPath(path) || isChatPath(path) || isResponsesPath(path) || isResponsesCompactPath(path)
}

// authorizeAdmin gates the console and the management API.
func (s *Server) authorizeAdmin(writer http.ResponseWriter, request *http.Request) bool {
	expected := s.store.AdminKey()
	if expected == "" {
		return s.authorizeOpen(writer, request)
	}
	client := throttleClient(request, s.store.AccessPolicy().TrustedProxies)
	if delay, blocked := s.adminThrottle.blocked(client); blocked {
		writeThrottled(writer, delay)
		return false
	}
	presented := strings.TrimSpace(request.Header.Get("X-Admin-Key"))
	if presented == "" {
		presented = bearerToken(request)
	}
	if constantTimeEqual(presented, expected) {
		s.adminThrottle.succeed(client)
		return true
	}
	if presented == "" {
		// An empty credential is a missing configuration, not a guess. The
		// console probes /api/* before login, and counting that would lock the
		// operator out of their own panel for 30 seconds.
		writeJSON(writer, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"message": "unauthorized: 管理密钥缺失或错误", "type": "auth_error"},
		})
		return false
	}
	if delay := s.adminThrottle.fail(client); delay > 0 {
		writeThrottled(writer, delay)
		return false
	}
	writeJSON(writer, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{"message": "unauthorized: 管理密钥缺失或错误", "type": "auth_error"},
	})
	return false
}

// authorizeClient gates the model endpoints. Three credentials open them: the
// master proxy key, an issued key with no limits, and an issued key whose
// account pin and spend limit still allow the request.
func (s *Server) authorizeClient(writer http.ResponseWriter, request *http.Request) bool {
	master := s.store.ProxyKey()
	issued := s.store.ProxyKeys()
	if master == "" && len(issued) == 0 {
		return s.authorizeOpen(writer, request)
	}
	client := throttleClient(request, s.store.AccessPolicy().TrustedProxies)
	if delay, blocked := s.clientThrottle.blocked(client); blocked {
		writeThrottled(writer, delay)
		return false
	}
	presented := bearerToken(request)
	if presented == "" {
		presented = strings.TrimSpace(request.Header.Get("X-Admin-Key"))
	}
	if constantTimeEqual(presented, master) {
		s.clientThrottle.succeed(client)
		return true
	}
	grant, found := s.store.FindProxyKey(presented)
	if !found {
		if presented == "" {
			// See authorizeAdmin: a client that has not been configured yet
			// must not consume the client's throttle budget.
			writeJSON(writer, http.StatusUnauthorized, map[string]any{
				"error": map[string]any{"message": "unauthorized: 代理密钥缺失或错误", "type": "auth_error"},
			})
			return false
		}
		if delay := s.clientThrottle.fail(client); delay > 0 {
			writeThrottled(writer, delay)
			return false
		}
		writeJSON(writer, http.StatusUnauthorized, map[string]any{
			"error": map[string]any{"message": "unauthorized: 代理密钥缺失或错误", "type": "auth_error"},
		})
		return false
	}
	if !grant.Enabled {
		writeJSON(writer, http.StatusForbidden, map[string]any{
			"error": map[string]any{
				"message": "该代理密钥已被禁用",
				"type":    "auth_error",
				"code":    "key_disabled",
			},
		})
		return false
	}
	if message := s.keyLimitMessage(grant); message != "" {
		writeKeyLimit(writer, "exceeded", message)
		return false
	}
	hold, message := s.spendHolds.admit(grant, s.store.KeyUsage()[grant.ID])
	if message != "" {
		writeKeyLimit(writer, "reserved", message)
		return false
	}
	s.clientThrottle.succeed(client)
	ctx := withCallerKey(request.Context(), callerKey{
		ID: grant.ID, Name: grant.Name, AccountID: grant.AccountID, Issued: true,
	})
	ctx = withSpendHold(ctx, hold)
	// A pinned key never falls back to another account: the operator promised
	// the holder this specific pool entry, and silent failover would spend
	// somebody else's budget instead.
	ctx = upstream.WithAccountPin(ctx, grant.AccountID)
	*request = *request.WithContext(ctx)
	return true
}

func writeKeyLimit(writer http.ResponseWriter, reason, message string) {
	writer.Header().Set("X-Cline-Key-Limit", reason)
	writeJSON(writer, http.StatusTooManyRequests, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "insufficient_quota",
			"code":    "key_spend_limit",
		},
	})
}

// keyLimitMessage reports why an issued key may not run, or an empty string
// when it may.
func (s *Server) keyLimitMessage(grant model.ProxyKeyGrant) string {
	usage := s.store.KeyUsage()[grant.ID]
	if grant.SpendLimitUSD > 0 && usage.SpentUSD() >= grant.SpendLimitUSD {
		return "该代理密钥的额度已用尽（限额 " + formatUSD(grant.SpendLimitUSD) +
			"，已用 " + formatUSD(usage.SpentUSD()) + "），请联系管理员提额或改用新密钥"
	}
	if grant.AccountID != "" {
		account := s.store.FindAccount(grant.AccountID)
		if account.Key == "" || !account.Enabled {
			return "该代理密钥绑定的账号当前不可用（已禁用或已删除）"
		}
	}
	return ""
}

// openRequestAllowed requires a trusted local connection whenever this API
// surface has no credential. A key for the other surface must not disable
// this boundary.
func (s *Server) openRequestAllowed(request *http.Request) bool {
	policy := s.store.AccessPolicy()
	return localRequestTrusted(request, policy) && loopbackHost(request.Host) &&
		s.browserRequestAllowed(request)
}

// authorizeOpen preserves local access to an API with no credential configured.
func (s *Server) authorizeOpen(writer http.ResponseWriter, request *http.Request) bool {
	if s.openRequestAllowed(request) {
		return true
	}
	writeJSON(writer, http.StatusForbidden, map[string]any{"error": map[string]any{"message": "untrusted request origin or host; non-local access requires PROXY_KEY", "type": "access_error"}})
	return false
}

// adminRequestAuthorized reports whether the request carries the console key.
// It is the read-only half of authorizeAdmin, used by the few handlers that
// answer both authenticated and unauthenticated callers with different detail.
func (s *Server) adminRequestAuthorized(request *http.Request) bool {
	expected := s.store.AdminKey()
	if expected == "" {
		return s.openRequestAllowed(request)
	}
	presented := strings.TrimSpace(request.Header.Get("X-Admin-Key"))
	if presented == "" {
		presented = bearerToken(request)
	}
	return constantTimeEqual(presented, expected)
}
