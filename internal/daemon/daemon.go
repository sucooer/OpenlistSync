package daemon

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"openlist-sync/internal/client"
	"openlist-sync/internal/config"
	"openlist-sync/internal/syncer"
)

type status struct {
	mu       sync.Mutex
	lastRun  time.Time
	lastErr  string
	running  bool
	runCount int
}

func (st *status) snapshot() (time.Time, string, bool, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.lastRun, st.lastErr, st.running, st.runCount
}

func Run(ctx context.Context, cfg *config.Config, logf func(string, ...any)) error {
	st := &status{}

	if cfg.HealthAddr != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			last, lastErr, running, n := st.snapshot()
			if lastErr != "" {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			fmt.Fprintf(w, "ok runs=%d running=%t last_run=%s last_error=%s\n", n, running, last.Format(time.RFC3339), lastErr)
		})
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			last, lastErr, _, n := st.snapshot()
			fmt.Fprintf(w, "openlist-sync daemon\nruns=%d last_run=%s last_error=%s\n", n, last.Format(time.RFC3339), lastErr)
		})
		srv := &http.Server{Addr: cfg.HealthAddr, Handler: mux}
		go func() {
			logf("health endpoint listening on %s", cfg.HealthAddr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logf("health server: %v", err)
			}
		}()
		defer srv.Shutdown(ctx)
	}

	run := func() {
		st.mu.Lock()
		if st.running {
			st.mu.Unlock()
			logf("previous sync still running, skipping this tick")
			return
		}
		st.running = true
		st.mu.Unlock()

		start := time.Now()
		s := syncer.New(cfg, client.New(cfg.BaseURL, cfg.Token, cfg.Username, cfg.Password, cfg.DownloadMode == "proxy", logf), logf)
		err := s.Run(ctx)

		st.mu.Lock()
		st.lastRun = time.Now()
		st.runCount++
		st.running = false
		if err != nil {
			st.lastErr = err.Error()
		} else {
			st.lastErr = ""
		}
		st.mu.Unlock()

		if err != nil {
			logf("sync finished with errors in %s: %v", time.Since(start).Round(time.Millisecond), err)
		} else {
			logf("sync finished cleanly in %s", time.Since(start).Round(time.Millisecond))
		}
	}

	logf("daemon started, interval=%s", cfg.Interval)
	run() // run once immediately, then tick
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			run()
		case <-ctx.Done():
			logf("shutting down")
			return nil
		}
	}
}