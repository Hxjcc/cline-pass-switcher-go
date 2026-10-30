package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"unicode/utf8"
)

var errDecodeConcurrency = errors.New("request decoding concurrency limit reached")

type decodeLeaseKey struct{}
type decodeLease struct {
	once    sync.Once
	release func()
}

func releaseDecoding(ctx context.Context) {
	if lease, ok := ctx.Value(decodeLeaseKey{}).(*decodeLease); ok {
		lease.once.Do(lease.release)
	}
}

func (s *Server) reserveDecode() (func(), error) {
	select {
	case s.decodeSlots <- struct{}{}:
		return func() { <-s.decodeSlots }, nil
	default:
		return nil, errDecodeConcurrency
	}
}

func (s *Server) readJSON(request *http.Request, target any) error {
	if request.Context().Value(decodeLeaseKey{}) == nil {
		release, err := s.reserveDecode()
		if err != nil {
			return err
		}
		defer release()
	}
	if err := readJSONLimit(request, target, s.maxRequestBytes); err != nil {
		return err
	}
	if body, ok := target.(*map[string]any); ok {
		return validateRequestIdentifiers(*body)
	}
	return nil
}

func validateRequestIdentifiers(body map[string]any) error {
	for _, key := range []string{"model", "prompt_cache_key", "safety_identifier"} {
		if value, ok := body[key].(string); ok && utf8.RuneCountInString(value) > 256 {
			return errors.New(key + " must not exceed 256 characters")
		}
	}
	return nil
}
