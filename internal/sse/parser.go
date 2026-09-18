// Package sse provides bounded, incremental SSE framing shared by all consumers.
package sse

import "errors"

const MaxEventBytes = 8 << 20

var ErrEventTooLarge = errors.New("upstream SSE event exceeds 8 MiB limit")

// Parser accepts LF, CRLF (including split CRLF), and CR line endings.
// Each input byte is inspected once. The zero value is ready to use.
type Parser struct {
	buf       []byte
	lineStart int
	skipLF    bool
	err       error
}

// Feed calls emit for each complete event. Returning false stops this feed.
func (p *Parser) Feed(data []byte, emit func(string) bool) error {
	if p.err != nil {
		return p.err
	}
	for _, b := range data {
		if p.skipLF {
			p.skipLF = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' {
			p.skipLF = true
			b = '\n'
		}
		if b == '\n' && len(p.buf) == p.lineStart {
			block := string(p.buf)
			if len(block) > 0 {
				block = block[:len(block)-1]
			}
			p.buf = p.buf[:0]
			p.lineStart = 0
			if !emit(block) {
				return nil
			}
			continue
		}
		if len(p.buf) >= MaxEventBytes {
			p.err = ErrEventTooLarge
			p.buf = nil
			return p.err
		}
		p.buf = append(p.buf, b)
		if b == '\n' {
			p.lineStart = len(p.buf)
		}
	}
	return nil
}

func (p *Parser) Finish() string {
	block := string(p.buf)
	p.buf = nil
	p.lineStart = 0
	return block
}
