package web

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Run starts the OpenListSync web UI + scheduler and blocks until ctx is done.
func Run(ctx context.Context, listen, storePath, apiToken, version string, logf func(string, ...any)) error {
	store, err := LoadStore(storePath)
	if err != nil {
		return err
	}
	logs := NewLogBuffer(2000)
	runner := NewRunner(ctx, store, logs)

	srv := NewServer(store, runner, logs, apiToken, version)
	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go runner.ScheduleLoop(ctx, 30*time.Second, logf)

	errCh := make(chan error, 1)
	go func() {
		logf("OpenListSync web 界面: http://%s", listen)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		// drain a possible serve error without leaking the goroutine
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		default:
		}
		return nil
	case err := <-errCh:
		return err
	}
}