package httpapi

import (
	"errors"
	"net/http"
	"time"
)

// A fresh budget covers both Write and Flush, rather than the entire stream.
func writeStreamChunk(writer http.ResponseWriter, data []byte) (int, error) {
	controller := http.NewResponseController(writer)
	if err := controller.SetWriteDeadline(time.Now().Add(streamClientWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	n, err := writer.Write(data)
	if err == nil {
		err = controller.Flush()
		if errors.Is(err, http.ErrNotSupported) {
			err = nil
		}
	}
	return n, err
}

func clearStreamDeadline(writer http.ResponseWriter) {
	_ = http.NewResponseController(writer).SetWriteDeadline(time.Time{})
}
