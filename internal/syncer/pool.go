package syncer

import (
	"context"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// RateLimiter is a simple token bucket (bytes/sec).
type RateLimiter struct {
	limit  float64
	tokens float64
	mu     sync.Mutex
	stop   chan struct{}
}

func NewRateLimiter(limit int64) *RateLimiter {
	if limit <= 0 {
		return nil
	}
	r := &RateLimiter{limit: float64(limit), stop: make(chan struct{})}
	go r.refill()
	return r
}

func (r *RateLimiter) refill() {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			r.mu.Lock()
			r.tokens = math.Min(r.tokens+r.limit*0.1, r.limit)
			r.mu.Unlock()
		case <-r.stop:
			return
		}
	}
}

func (r *RateLimiter) Stop() { select { case <-r.stop: default: close(r.stop) } }

func (r *RateLimiter) Wait(n int64) {
	for n > 0 {
		step := n
		if lim := int64(r.limit); step > lim {
			step = lim // never starve on requests larger than the bucket
		}
		for {
			r.mu.Lock()
			if r.tokens >= float64(step) {
				r.tokens -= float64(step)
				r.mu.Unlock()
				break
			}
			r.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
		n -= step
	}
}

const rateChunk = 32 << 10 // max bytes charged to the limiter at once

func (r *RateLimiter) Chunk() int {
	if r.limit < rateChunk {
		return int(r.limit)
	}
	return rateChunk
}

type rateWriter struct {
	w io.Writer
	l *RateLimiter
}

func (r *rateWriter) Write(p []byte) (int, error) {
	chunk := r.l.Chunk()
	var total int
	for len(p) > 0 {
		n := len(p)
		if n > chunk {
			n = chunk
		}
		r.l.Wait(int64(n))
		if _, err := r.w.Write(p[:n]); err != nil {
			return total, err
		}
		p = p[n:]
		total += n
	}
	return total, nil
}

type rateReader struct {
	r io.Reader
	l *RateLimiter
}

func (r *rateReader) Read(p []byte) (int, error) {
	chunk := r.l.Chunk()
	if len(p) > chunk {
		p = p[:chunk]
	}
	n, err := r.r.Read(p)
	if n > 0 {
		r.l.Wait(int64(n))
	}
	return n, err
}

// Pool runs functions concurrently, returning the failure count.
func runPool(ctx context.Context, workers int, fns []func(ctx context.Context) error) int {
	if workers < 1 {
		workers = 1
	}
	var fail atomic.Int32
	ch := make(chan func(ctx context.Context) error)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fn := range ch {
				if ctx.Err() != nil {
					continue
				}
				if err := fn(ctx); err != nil {
					fail.Add(1)
				}
			}
		}()
	}
	for _, fn := range fns {
		if ctx.Err() != nil {
			break
		}
		ch <- fn
	}
	close(ch)
	wg.Wait()
	return int(fail.Load())
}