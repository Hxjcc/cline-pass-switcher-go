package httpapi

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The store holds an exclusive data-directory lock before this cleanup runs.
func prepareReplayDirectory(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "stream-replay")
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("stream-replay must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cline-pass-replay-") && !entry.IsDir() {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return "", err
			}
		}
	}
	return dir, nil
}
