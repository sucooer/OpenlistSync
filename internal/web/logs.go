package web

import (
	"fmt"
	"sync"
	"time"
)

// LogLine is a single captured log entry.
type LogLine struct {
	Time string `json:"time"`
	Msg  string `json:"msg"`
}

// LogBuffer keeps the last N log lines (oldest first).
type LogBuffer struct {
	mu   sync.Mutex
	max  int
	seq  int
	reqs map[int]bool // active stream request markers (unused, kept simple)
	lines []LogLine
}

func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 2000
	}
	return &LogBuffer{max: max, reqs: map[int]bool{}}
}

func (l *LogBuffer) Add(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, LogLine{Time: time.Now().Format("15:04:05.000"), Msg: msg})
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
}

func (l *LogBuffer) Snapshot(n int) []LogLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.lines) {
		n = len(l.lines)
	}
	out := make([]LogLine, n)
	copy(out, l.lines[len(l.lines)-n:])
	return out
}

func (l *LogBuffer) Clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = nil
}

// LoggerFn builds a log sink for a named runner that feeds the buffer and Stdout.
func (l *LogBuffer) LoggerFn(prefix string) func(string, ...any) {
	return func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		if prefix != "" {
			msg = "[" + prefix + "] " + msg
		}
		l.Add(msg)
	}
}