package syncer

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterThrottles(t *testing.T) {
	r := NewRateLimiter(1000)
	defer r.Stop()
	start := time.Now()
	var total int64
	for i := 0; i < 30; i++ {
		n := int64(100)
		r.Wait(n)
		total += n
	}
	elapsed := time.Since(start)
	// 3000 bytes at 1000 B/s should take ~3s
	if elapsed < 2*time.Second || elapsed > 6*time.Second {
		t.Fatalf("expected ~3s for 3000B at 1000B/s, got %s", elapsed)
	}
}

func TestRateLimiterLargeRequestCompletes(t *testing.T) {
	// a request larger than the bucket capacity must still complete (at rate)
	r := NewRateLimiter(1000)
	defer r.Stop()
	start := time.Now()
	r.Wait(5000)
	elapsed := time.Since(start)
	if elapsed < 3*time.Second || elapsed > 8*time.Second {
		t.Fatalf("5000B at 1000B/s should take ~5s, got %s", elapsed)
	}
}

func TestRunPoolRunsAll(t *testing.T) {
	var n atomic.Int32
	var fns []func(ctx context.Context) error
	for i := 0; i < 20; i++ {
		i := i
		fns = append(fns, func(ctx context.Context) error {
			n.Add(1)
			_ = i
			return nil
		})
	}
	fails := runPool(context.Background(), 5, fns)
	if fails != 0 || n.Load() != 20 {
		t.Fatalf("fails=%d ran=%d", fails, n.Load())
	}
}

func TestRunPoolCountsFailures(t *testing.T) {
	var fns []func(ctx context.Context) error
	for i := 0; i < 4; i++ {
		fns = append(fns, func(ctx context.Context) error {
			return fmt.Errorf("boom")
		})
	}
	if fails := runPool(context.Background(), 2, fns); fails != 4 {
		t.Fatalf("expected 4 failures, got %d", fails)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:             "512B",
		1024:            "1.0KiB",
		1536:            "1.5KiB",
		5 * 1024 * 1024: "5.0MiB",
	}
	for in, want := range cases {
		if got := humanSize(in); !strings.HasPrefix(got, want) {
			t.Errorf("humanSize(%d) = %q, want prefix %q", in, got, want)
		}
	}
}