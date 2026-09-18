package httpapi

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

const replayBlockBytes = 32 << 10

var errReplayLimit = errors.New("stream replay storage limit exceeded")

type replayLimits struct {
	memoryPerStream int64
	memoryTotal     int64
	bytesPerStream  int64
	bytesTotal      int64
	tempDir         string
}

func defaultReplayLimits() replayLimits {
	return replayLimits{
		memoryPerStream: 1 << 20, memoryTotal: 32 << 20,
		bytesPerStream: 128 << 20, bytesTotal: 1 << 30,
	}
}

// The memory budget counts allocated block capacity, not just payload length.
// The storage budget counts all retained wire bytes, in memory or on disk.
type replayBudget struct {
	mu             sync.Mutex
	limits         replayLimits
	memory, stored int64
}

func (b *replayBudget) reserveMemory(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.limits.memoryTotal-b.memory {
		return false
	}
	b.memory += n
	return true
}

func (b *replayBudget) reserveStorage(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.limits.bytesTotal-b.stored {
		return false
	}
	b.stored += n
	return true
}

func (b *replayBudget) release(memory, stored int64) {
	b.mu.Lock()
	b.memory -= memory
	b.stored -= stored
	b.mu.Unlock()
}

// replayLog retains a complete SSE byte stream. Spilling does not change read
// offsets, so a reconnect can still start at byte zero without re-generating.
// There is no per-event index and no retained JSON object graph.
type replayLog struct {
	mu               sync.Mutex
	budget           *replayBudget
	blocks           [][]byte
	file             *os.File
	size             int64
	changed          chan struct{}
	closed, disposed bool
	err              error
}

func newReplayLog(budget *replayBudget) *replayLog {
	return &replayLog{budget: budget, changed: make(chan struct{})}
}

func (log *replayLog) signalLocked() {
	close(log.changed)
	log.changed = make(chan struct{})
}

func (log *replayLog) failLocked(err error) (int, error) {
	log.err, log.closed = err, true
	log.signalLocked()
	return 0, err
}

func (log *replayLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.err != nil {
		return 0, log.err
	}
	if log.closed || log.disposed {
		return 0, io.ErrClosedPipe
	}
	n := int64(len(data))
	if n == 0 {
		return 0, nil
	}
	if n > log.budget.limits.bytesPerStream-log.size || !log.budget.reserveStorage(n) {
		return log.failLocked(errReplayLimit)
	}
	if log.file == nil {
		capacity := int64(len(log.blocks) * replayBlockBytes)
		needed := ((log.size+n+replayBlockBytes-1)/replayBlockBytes)*replayBlockBytes - capacity
		if capacity+needed > log.budget.limits.memoryPerStream || !log.budget.reserveMemory(needed) {
			if err := log.spillLocked(); err != nil {
				log.budget.release(0, n)
				return log.failLocked(fmt.Errorf("create stream replay file: %w", err))
			}
		} else {
			for allocated := int64(0); allocated < needed; allocated += replayBlockBytes {
				log.blocks = append(log.blocks, make([]byte, replayBlockBytes))
			}
		}
	}
	if log.file != nil {
		count, err := log.file.WriteAt(data, log.size)
		if err == nil && count != len(data) {
			err = io.ErrShortWrite
		}
		if err != nil {
			log.budget.release(0, n)
			return log.failLocked(fmt.Errorf("write stream replay file: %w", err))
		}
	} else {
		for offset, copied := log.size, 0; copied < len(data); {
			block, pos := offset/replayBlockBytes, offset%replayBlockBytes
			count := copy(log.blocks[block][pos:], data[copied:])
			copied += count
			offset += int64(count)
		}
	}
	log.size += n
	log.signalLocked()
	return len(data), nil
}

func (log *replayLog) spillLocked() error {
	file, err := os.CreateTemp(log.budget.limits.tempDir, "cline-pass-replay-*")
	if err != nil {
		return err
	}
	for index, block := range log.blocks {
		offset := int64(index * replayBlockBytes)
		length := min(int64(len(block)), log.size-offset)
		count, err := file.WriteAt(block[:length], offset)
		if err == nil && int64(count) != length {
			err = io.ErrShortWrite
		}
		if err != nil {
			file.Close()
			os.Remove(file.Name())
			return err
		}
	}
	log.file = file
	log.budget.release(int64(len(log.blocks)*replayBlockBytes), 0)
	log.blocks = nil
	return nil
}

// read returns at most one block. A non-nil wait channel means the caller has
// caught up; closing that channel wakes it without missed cancellation signals.
func (log *replayLog) read(offset int64) ([]byte, <-chan struct{}, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.disposed {
		return nil, nil, io.ErrClosedPipe
	}
	if offset < log.size {
		data := make([]byte, min(int64(replayBlockBytes), log.size-offset))
		if log.file != nil {
			if _, err := log.file.ReadAt(data, offset); err != nil {
				return nil, nil, err
			}
		} else {
			for copied := 0; copied < len(data); {
				block, pos := offset/replayBlockBytes, offset%replayBlockBytes
				count := copy(data[copied:], log.blocks[block][pos:])
				copied += count
				offset += int64(count)
			}
		}
		return data, nil, nil
	}
	if log.err != nil {
		return nil, nil, log.err
	}
	if log.closed {
		return nil, nil, io.EOF
	}
	return nil, log.changed, nil
}

func (log *replayLog) finish() {
	log.mu.Lock()
	log.closed = true
	log.signalLocked()
	log.mu.Unlock()
}

func (log *replayLog) failure() error {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.err
}

func (log *replayLog) abort(err error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.err == nil {
		log.failLocked(err)
	}
}

// Called only after the runner and every subscriber have released the log.
func (log *replayLog) dispose() {
	log.mu.Lock()
	defer log.mu.Unlock()
	if log.disposed {
		return
	}
	log.disposed, log.closed = true, true
	if log.file != nil {
		name := log.file.Name()
		log.file.Close()
		os.Remove(name)
		log.file = nil
	}
	log.budget.release(int64(len(log.blocks)*replayBlockBytes), log.size)
	log.blocks, log.size = nil, 0
	log.signalLocked()
}
