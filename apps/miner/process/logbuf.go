package process

import "sync"

const logBufSize = 2000

type LogBuf struct {
	mu    sync.Mutex
	lines [logBufSize]string
	head  int // index of next write slot
	count int // total written (capped at logBufSize for ring)
	emit  func(string)
}

func newLogBuf(emit func(string)) *LogBuf {
	return &LogBuf{emit: emit}
}

func (b *LogBuf) Append(line string) {
	b.mu.Lock()
	b.lines[b.head] = line
	b.head = (b.head + 1) % logBufSize
	if b.count < logBufSize {
		b.count++
	}
	b.mu.Unlock()
	if b.emit != nil {
		b.emit(line)
	}
}

// Last returns the most recent n lines (oldest first). n is clamped to count.
func (b *LogBuf) Last(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.count {
		n = b.count
	}
	if n == 0 {
		return nil
	}
	out := make([]string, n)
	// start slot = head - n (mod logBufSize)
	start := (b.head - n + logBufSize*2) % logBufSize
	for i := 0; i < n; i++ {
		out[i] = b.lines[(start+i)%logBufSize]
	}
	return out
}
