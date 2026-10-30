package httpapi

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	responsesbridge "github.com/Hxjcc/cline-pass-switcher-go/internal/responses"
)

// This is a proxy encryption key, not stored conversation history.
func loadReasoningCodec(dir string) (*responsesbridge.ReasoningCodec, error) {
	path := filepath.Join(dir, "reasoning.key")
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || info.Size() != 32 {
			return nil, errors.New("reasoning.key must be a regular 32-byte key file")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, err
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return responsesbridge.NewReasoningCodec(key)
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, writeErr := f.Write(key)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return responsesbridge.NewReasoningCodec(key)
}

func (s *Server) requestReasoningCodec(ctx context.Context) *responsesbridge.ReasoningCodec {
	scope := "master"
	if caller, ok := callerKeyFrom(ctx); ok && caller.Issued {
		scope = "issued:" + caller.ID
	}
	return s.reasoningCodec.Scoped(scope)
}
