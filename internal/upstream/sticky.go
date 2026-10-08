package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/jsonx"
	"github.com/munmunjaklin458-afk/cline-pass-switcher-go/internal/model"
)

const (
	// defaultSessionStickTTL should roughly match how long a provider keeps its
	// prompt cache: a shorter value drops a warm account, a longer one only
	// costs a few bytes per conversation.
	defaultSessionStickTTL = 60 * time.Minute
	// stickSweepInterval bounds how often expired conversations are dropped.
	// Without a sweep an entry only disappears when that same session comes
	// back, so threads the user abandoned stay in memory forever.
	stickSweepInterval = 5 * time.Minute
)

// stickTTL is the configured conversation lifetime, falling back to the
// built-in default for an empty or unparseable value.
func (s *Service) stickTTL() time.Duration {
	raw := strings.TrimSpace(s.store.Config().StickTTL)
	if raw == "" {
		return defaultSessionStickTTL
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return defaultSessionStickTTL
	}
	return ttl
}

// sessionStick is the account and pinned channel that last completed a
// conversation. It lives in memory: losing it only makes the next turn cold.
type sessionStick struct {
	accountID string
	upstream  string
	keyHash   string
	until     time.Time
}

type stickContextKey struct{}

// accountPinContextKey carries a hard restriction to one account pool entry.
// Issued keys use it: the operator promised the holder that specific account,
// so the request must not silently drift to another one when it is busy.
type accountPinContextKey struct{}

// WithAccountPin restricts the request to one account. An empty id leaves the
// context untouched, which is what the master key and the console want.
func WithAccountPin(ctx context.Context, accountID string) context.Context {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, accountPinContextKey{}, accountID)
}

// AccountPinFrom reports the account a request is pinned to.
func AccountPinFrom(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	accountID, ok := ctx.Value(accountPinContextKey{}).(string)
	return accountID, ok && accountID != ""
}

type stickHint struct {
	session   string
	accountID string
}

// sessionHeaderNames are the out-of-band conversation ids clients send when
// they do not put prompt_cache_key in the body. pi sends session_id and
// x-session-affinity once its affinity headers are enabled, OpenRouter-style
// clients use x-session-id, and Codex sends session-id next to thread-id and
// x-client-request-id (dashes, so the underscore spelling never matches it).
// Order is priority: the conversation header wins over the thread header,
// because a Codex subagent reports its parent as session-id and its own id as
// thread-id, and a subagent belongs to the conversation that spawned it.
var sessionHeaderNames = []string{
	"session_id",
	"session-id",
	"x-session-affinity",
	"x-session-id",
	"thread-id",
	"x-client-request-id",
}

// maxSessionHintLen caps how much of a header value becomes a memory key. A
// long id is hashed rather than truncated so two conversations can never share
// a key through a common prefix.
const maxSessionHintLen = 128

// SessionIDFromHeaders returns the conversation id a client sent in headers,
// or "" when none of the session headers is present.
func SessionIDFromHeaders(header http.Header) string {
	for _, name := range sessionHeaderNames {
		id := strings.TrimSpace(header.Get(name))
		if id == "" {
			continue
		}
		if len(id) > maxSessionHintLen {
			sum := sha256.Sum256([]byte(id))
			return "h" + hex.EncodeToString(sum[:])
		}
		return id
	}
	return ""
}

type sessionHintContextKey struct{}

// WithSessionHint attaches a header-derived conversation id so the sticky
// layer can use it on requests that carry no prompt_cache_key in the body.
func WithSessionHint(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if ctx == nil || id == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionHintContextKey{}, id)
}

func sessionHintFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionHintContextKey{}).(string)
	return id
}

// SessionKey identifies one conversation for stickiness. Only the client's
// prompt_cache_key counts. Codex sends that on every turn of a thread; a
// shared system prompt or user field would glue unrelated chats together.
func SessionKey(modelID string, body map[string]any) string {
	key := strings.TrimSpace(jsonx.String(body["prompt_cache_key"]))
	if key == "" {
		return ""
	}
	return "cache\n" + strings.TrimSpace(modelID) + "\n" + key
}

// SessionKeyFor resolves the conversation key the same way as SessionKey, and
// falls back to the session hint from headers when the body carries none. The
// two share one key space, so a client that switches between the body field
// and the headers keeps the same warm conversation.
func SessionKeyFor(ctx context.Context, modelID string, body map[string]any) string {
	if key := SessionKey(modelID, body); key != "" {
		return key
	}
	hint := sessionHintFrom(ctx)
	if hint == "" {
		return ""
	}
	return "cache\n" + strings.TrimSpace(modelID) + "\n" + hint
}

// WithStick attaches the conversation identity and its current account choice
// to the request context. Child timeouts keep the values.
func WithStick(ctx context.Context, session, accountID string) context.Context {
	if session == "" {
		return ctx
	}
	return context.WithValue(ctx, stickContextKey{}, stickHint{session: session, accountID: accountID})
}

func stickHintFrom(ctx context.Context) stickHint {
	if ctx == nil {
		return stickHint{}
	}
	hint, _ := ctx.Value(stickContextKey{}).(stickHint)
	return hint
}

// LookupStick returns the account and pinned channel saved for a conversation.
// An empty session, an expired entry, or a rotated key is a miss.
func (s *Service) LookupStick(session string) (accountID, upstream string) {
	if session == "" {
		return "", ""
	}
	s.stickMu.Lock()
	stick, found := s.sticks[session]
	if found && !time.Now().Before(stick.until) {
		delete(s.sticks, session)
		found = false
	}
	s.stickMu.Unlock()
	if !found {
		return "", ""
	}
	account := s.store.FindAccount(stick.accountID)
	if account.Key == "" || accountKeyHash(account.Key) != stick.keyHash {
		s.forgetStick(session)
		return "", ""
	}
	return stick.accountID, stick.upstream
}

func (s *Service) rememberStick(session string, account model.Account, upstream string) {
	if session == "" || account.ID == "" || account.Key == "" {
		return
	}
	now := time.Now()
	s.stickMu.Lock()
	defer s.stickMu.Unlock()
	if s.sticks == nil {
		s.sticks = map[string]sessionStick{}
	}
	s.sweepSticksLocked(now)
	s.sticks[session] = sessionStick{
		accountID: account.ID,
		upstream:  upstream,
		keyHash:   accountKeyHash(account.Key),
		until:     now.Add(s.stickTTL()),
	}
}

// sweepSticksLocked drops conversations whose time to live elapsed. The
// interval keeps a busy service from walking the map on every response.
func (s *Service) sweepSticksLocked(now time.Time) {
	if !s.stickSweepAt.IsZero() && now.Sub(s.stickSweepAt) < stickSweepInterval {
		return
	}
	s.stickSweepAt = now
	for session, stick := range s.sticks {
		if !now.Before(stick.until) {
			delete(s.sticks, session)
		}
	}
}

func (s *Service) forgetStick(session string) {
	if session == "" {
		return
	}
	s.stickMu.Lock()
	defer s.stickMu.Unlock()
	delete(s.sticks, session)
}

func (s *Service) observeStick(ctx context.Context, account model.Account, upstream string, status int) {
	hint := stickHintFrom(ctx)
	if hint.session == "" {
		return
	}
	switch status {
	case http.StatusOK:
		s.rememberStick(hint.session, account, upstream)
	case http.StatusUnauthorized, http.StatusForbidden:
		s.forgetStick(hint.session)
	}
}

// PreferAttempt moves the conversation's last pinned channel to the front of
// the failover list. Auto routes have a single empty attempt, so this is a
// no-op there.
func PreferAttempt(attempts []Attempt, upstream string) []Attempt {
	if upstream == "" || len(attempts) < 2 {
		return attempts
	}
	for index, attempt := range attempts {
		if attempt.Upstream != upstream {
			continue
		}
		if index == 0 {
			return attempts
		}
		ordered := make([]Attempt, 0, len(attempts))
		ordered = append(ordered, attempt)
		ordered = append(ordered, attempts[:index]...)
		ordered = append(ordered, attempts[index+1:]...)
		return ordered
	}
	return attempts
}
