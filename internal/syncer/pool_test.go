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
	fails, err := runPool(context.Background(), 5, fns)
	if fails != 0 || n.Load() != 20 || err != nil {
		t.Fatalf("fails=%d ran=%d err=%v", fails, n.Load(), err)
	}
}

func TestRunPoolCountsFailures(t *testing.T) {
	var fns []func(ctx context.Context) error
	for i := 0; i < 4; i++ {
		fns = append(fns, func(ctx context.Context) error {
			return fmt.Errorf("boom")
		})
	}
	fails, err := runPool(context.Background(), 2, fns)
	if fails != 4 {
		t.Fatalf("expected 4 failures, got %d", fails)
	}
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected first error 'boom', got %v", err)
	}
}

func TestRunPoolFiltersContextErrors(t *testing.T) {
	// Mix of real errors and context.Canceled; the real error must surface,
	// the context cancellation must NOT be reported as the run's failure cause.
	fns := []func(ctx context.Context) error{
		func(ctx context.Context) error { return context.Canceled },
		func(ctx context.Context) error { return fmt.Errorf("disk full") },
		func(ctx context.Context) error { return context.DeadlineExceeded },
	}
	fails, err := runPool(context.Background(), 3, fns)
	if fails != 3 {
		t.Fatalf("expected all 3 to count as fail, got %d", fails)
	}
	if err == nil || err.Error() != "disk full" {
		t.Fatalf("first non-ctx error should be 'disk full', got %v", err)
	}
}

func TestRunPoolNoErrorOnAllSuccess(t *testing.T) {
	fns := []func(ctx context.Context) error{
		func(ctx context.Context) error { return nil },
		func(ctx context.Context) error { return nil },
	}
	fails, err := runPool(context.Background(), 2, fns)
	if fails != 0 || err != nil {
		t.Fatalf("all success should yield 0 fails, nil err; got fails=%d err=%v", fails, err)
	}
}

func TestFormatTransferErrors(t *testing.T) {
	cases := []struct {
		name    string
		dlFail  int
		dlErr   error
		upFail  int
		upErr   error
		wantHas []string
	}{
		{
			name:    "only uploads",
			upFail:  3,
			upErr:   fmt.Errorf("403 forbidden"),
			wantHas: []string{"3 upload(s) failed", "403 forbidden"},
		},
		{
			name:    "only downloads",
			dlFail:  2,
			dlErr:   fmt.Errorf("connection reset"),
			wantHas: []string{"2 download(s) failed", "connection reset"},
		},
		{
			name:    "both phases",
			dlFail:  1,
			dlErr:   fmt.Errorf("timeout"),
			upFail:  4,
			upErr:   fmt.Errorf("permission denied"),
			wantHas: []string{"1 download(s) failed", "timeout", "4 upload(s) failed", "permission denied"},
		},
		{
			name:    "no captured error message",
			upFail:  2,
			wantHas: []string{"2 upload(s) failed"},
		},
		{
			name:    "no failures",
			wantHas: []string{""}, // empty string is the documented "all OK" signal
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTransferErrors(c.dlFail, c.dlErr, c.upFail, c.upErr)
			for _, want := range c.wantHas {
				if want == "" {
					if got != "" {
						t.Fatalf("expected empty string, got %q", got)
					}
					continue
				}
				if !strings.Contains(got, want) {
					t.Fatalf("output %q should contain %q", got, want)
				}
			}
		})
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