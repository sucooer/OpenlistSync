package syncer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"openlist-sync/internal/client"
	"openlist-sync/internal/config"
)

type Syncer struct {
	cfg     *config.Config
	client  *client.Client
	logf    func(format string, a ...any)
	verbose bool
	limiter *RateLimiter
}

func New(cfg *config.Config, c *client.Client, logf func(string, ...any)) *Syncer {
	return &Syncer{
		cfg:     cfg,
		client:  c,
		logf:    logf,
		verbose: cfg.Verbose,
		limiter: NewRateLimiter(cfg.RateLimit),
	}
}

func (s *Syncer) Run(ctx context.Context) error {
	if s.limiter != nil {
		defer s.limiter.Stop()
	}
	var failed int
	for _, t := range s.cfg.Tasks {
		if ctx.Err() != nil {
			break
		}
		if err := s.syncTask(ctx, t); err != nil {
			s.logf("task %s -> %s failed: %v", t.RemotePath, t.LocalDir, err)
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d task(s) failed", failed)
	}
	return nil
}

func (s *Syncer) syncTask(ctx context.Context, t config.Task) error {
	start := time.Now()
	s.logf("task: %s -> %s (direction=%s cleanup=%s conflict=%s)", t.RemotePath, t.LocalDir, s.cfg.Direction, s.cfg.Cleanup, s.cfg.Conflict)

	if _, err := os.Stat(t.LocalDir); err != nil {
		if err := os.MkdirAll(t.LocalDir, 0o755); err != nil {
			return fmt.Errorf("local dir: %w", err)
		}
	}

	remote, err := remoteTree(ctx, s.client, t.RemotePath)
	if err != nil {
		return fmt.Errorf("listing remote tree: %w", err)
	}
	local, err := localTree(t.LocalDir)
	if err != nil {
		return fmt.Errorf("scanning local tree: %w", err)
	}

	p := &planner{
		direction: s.cfg.Direction,
		cleanup:   s.cfg.Cleanup,
		conflict:  s.cfg.Conflict,
		filter:    NewFilter(s.cfg.IncludeExt, s.cfg.ExcludeExt, s.cfg.FileTypes),
	}
	plan := p.plan(remote, local)

	if len(plan.Jobs) == 0 {
		s.logf("  up to date")
		return nil
	}
	s.logf("  plan: +%d down, +%d up, %d mkdir-l, %d mkdir-r, -%d local, -%d remote",
		plan.Download, plan.Upload, plan.MkdirLocal, plan.MkdirRemote, plan.RmLocal, plan.RmRemote)

	if s.cfg.DryRun {
		for _, j := range plan.Jobs {
			s.logf("  dry-run: %s %s", j.Kind, j.Rel)
		}
		return nil
	}

	// phase 1: remote dirs first (uploads rely on them), then local dirs
	dirs := &remoteDirMaker{c: s.client, created: map[string]bool{}}
	for _, j := range plan.Jobs {
		if j.Kind != JobMkdirRemote {
			continue
		}
		if err := dirs.Ensure(ctx, path.Join(t.RemotePath, j.Rel)); err != nil {
			s.logf("  mkdir %s: %v", j.Rel, err)
		}
	}
	for _, j := range plan.Jobs {
		if j.Kind != JobMkdirLocal {
			continue
		}
		if err := os.MkdirAll(filepath.Join(t.LocalDir, filepath.FromSlash(j.Rel)), 0o755); err != nil {
			s.logf("  mkdir-local %s: %v", j.Rel, err)
		}
	}

	// phase 2: downloads in parallel
	var dlFns []func(ctx context.Context) error
	for _, j := range plan.Jobs {
		if j.Kind != JobDownload {
			continue
		}
		j := j
		dlFns = append(dlFns, func(ctx context.Context) error {
			return s.withRetry(ctx, func() error { return s.downloadOne(ctx, t, j) })
		})
	}
	if n := runPool(ctx, s.cfg.Concurrency, dlFns); n > 0 {
		s.logf("  %d download(s) failed", n)
	}

	// phase 3: uploads in parallel
	var upFns []func(ctx context.Context) error
	for _, j := range plan.Jobs {
		if j.Kind != JobUpload {
			continue
		}
		j := j
		upFns = append(upFns, func(ctx context.Context) error {
			return s.withRetry(ctx, func() error { return s.uploadOne(ctx, t, dirs, j) })
		})
	}
	if n := runPool(ctx, s.cfg.Concurrency, upFns); n > 0 {
		s.logf("  %d upload(s) failed", n)
	}

	// phase 4: removals
	for _, j := range plan.Jobs {
		if ctx.Err() != nil {
			break
		}
		switch j.Kind {
		case JobRmLocalFile:
			if err := os.Remove(filepath.Join(t.LocalDir, filepath.FromSlash(j.Rel))); err != nil {
				s.logf("  remove %s: %v", j.Rel, err)
			} else if s.verbose {
				s.logf("  removed %s", j.Rel)
			}
		case JobRmLocalDir:
			// only succeeds when empty; emptied dirs are removed deepest-first
			if err := os.Remove(filepath.Join(t.LocalDir, filepath.FromSlash(j.Rel))); err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					s.logf("  rmdir %s: %v", j.Rel, err)
				}
			}
		case JobRmRemote:
			if err := s.client.Remove(ctx, j.Parent, j.Names); err != nil {
				s.logf("  remove-remote %s: %v", j.Parent, err)
			} else if s.verbose {
				s.logf("  removed-remote %d file(s) under %s", len(j.Names), j.Parent)
			}
		}
	}

	s.logf("  done in %s", time.Since(start).Round(time.Millisecond))
	return nil
}

func (s *Syncer) withRetry(ctx context.Context, fn func() error) error {
	var err error
	for i := 0; i <= s.cfg.Retries; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err = fn()
		if err == nil {
			return nil
		}
		if i < s.cfg.Retries {
			delay := time.Duration(1<<i) * 500 * time.Millisecond
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
		}
	}
	return err
}

func (s *Syncer) downloadOne(ctx context.Context, t config.Task, j Job) error {
	rel := j.Rel
	localPath := filepath.Join(t.LocalDir, filepath.FromSlash(rel))
	part := localPath + partSuffix
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}

	var offset int64
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
		if offset == j.Remote.Obj.Size {
			return s.finalizeDownload(t, j, part, localPath)
		}
	}

	dl, err := s.client.OpenDownload(ctx, j.Remote.Path, j.Remote.Obj.Sign, offset)
	if err != nil {
		if errors.Is(err, client.ErrComplete) {
			return s.finalizeDownload(t, j, part, localPath)
		}
		if errors.Is(err, client.ErrNotFound) {
			return nil // vanished remotely, treat as success
		}
		return err
	}
	defer dl.Body.Close()

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if !dl.Resumed {
		if err := f.Truncate(0); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return err
		}
	}

	var w io.Writer = f
	if s.limiter != nil {
		w = &rateWriter{w: f, l: s.limiter}
	}
	written, err := io.CopyBuffer(w, dl.Body, make([]byte, 256<<10))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if dl.Resumed {
		written += offset
	}
	if j.Remote.Obj.Size > 0 && written != j.Remote.Obj.Size {
		os.Remove(part)
		return fmt.Errorf("size mismatch for %s: got %d want %d", rel, written, j.Remote.Obj.Size)
	}
	if err := s.finalizeDownload(t, j, part, localPath); err != nil {
		return err
	}
	s.logf("  downloaded %s (%s)", rel, humanSize(written))
	return nil
}

func (s *Syncer) finalizeDownload(t config.Task, j Job, part, final string) error {
	if err := os.Rename(part, final); err != nil {
		return err
	}
	if mt, ok := j.Remote.ModTime(); ok {
		_ = os.Chtimes(final, time.Now(), mt)
	}
	if s.verbose {
		s.logf("  completed %s", j.Rel)
	}
	return nil
}

func (s *Syncer) uploadOne(ctx context.Context, t config.Task, dirs *remoteDirMaker, j Job) error {
	rel := j.Rel
	localPath := filepath.Join(t.LocalDir, filepath.FromSlash(rel))
	remotePath := path.Join(t.RemotePath, rel)

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return nil
	}

	if err := dirs.Ensure(ctx, path.Dir(remotePath)); err != nil {
		return err
	}
	var src io.Reader = f
	if s.limiter != nil {
		src = &rateReader{r: f, l: s.limiter}
	}
	if err := s.client.Upload(ctx, localPath, remotePath, j.Overwrite, src, fi.Size()); err != nil {
		return err
	}
	// align local mtime with the server's so the next pass sees no change
	if info, err := s.client.GetInfo(ctx, remotePath); err == nil {
		if mt, ok := info.ModTime(); ok {
			_ = os.Chtimes(localPath, time.Now(), mt)
		}
	}
	s.logf("  uploaded %s (%s)", rel, humanSize(fi.Size()))
	return nil
}

// remoteDirMaker creates remote directories idempotently.
type remoteDirMaker struct {
	c       *client.Client
	mu      sync.Mutex
	created map[string]bool
}

func (m *remoteDirMaker) Ensure(ctx context.Context, dir string) error {
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	cur := ""
	for _, p := range parts {
		cur = path.Join(cur, p)
		m.mu.Lock()
		if m.created[cur] {
			m.mu.Unlock()
			continue
		}
		m.mu.Unlock()
		if err := m.c.Mkdir(ctx, cur); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already exists") {
				m.mu.Lock()
				m.created[cur] = true
				m.mu.Unlock()
				continue
			}
			return err
		}
		m.mu.Lock()
		m.created[cur] = true
		m.mu.Unlock()
	}
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}