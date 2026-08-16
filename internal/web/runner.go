package web

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"openlist-sync/internal/client"
	"openlist-sync/internal/config"
	"openlist-sync/internal/syncer"
)

// Runner executes tasks (on demand and on schedule).
type Runner struct {
	store   *Store
	logs    *LogBuffer
	root    context.Context // server lifecycle context (not request-scoped)
	running map[string]bool // task ID -> running
	mu      sync.Mutex
}

func NewRunner(ctx context.Context, store *Store, logs *LogBuffer) *Runner {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Runner{store: store, logs: logs, root: ctx, running: map[string]bool{}}
}

func (r *Runner) IsRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[id]
}

func (r *Runner) AnyRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.running {
		if v {
			return true
		}
	}
	return false
}

// RunningSet returns the task ids currently running.
func (r *Runner) RunningSet() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for id, v := range r.running {
		if v {
			out = append(out, id)
		}
	}
	return out
}

// buildConfig assembles a config.Config for a single task.
func (r *Runner) buildConfig(t *Task, c *Connection) (*config.Config, error) {
	if c == nil {
		return nil, fmt.Errorf("关联的 OpenList 连接不存在")
	}
	st := r.store.Snapshot().Settings
	cfg := &config.Config{
		BaseURL:      strings.TrimRight(c.BaseURL, "/"),
		Token:        c.Token,
		Username:     c.Username,
		Password:     c.Password,
		DownloadMode: c.DownloadMode,
		Direction:    t.Direction,
		Cleanup:      t.Cleanup,
		Conflict:     t.Conflict,
		IncludeExt:   t.IncludeExt,
		ExcludeExt:   t.ExcludeExt,
		FileTypes:    t.Types,
		Concurrency:  st.Concurrency,
		RateLimit:    t.RateLimit,
		Retries:      st.Retries,
		Tasks:        []config.Task{{RemotePath: t.RemotePath, LocalDir: t.LocalDir}},
	}
	if st.RateLimit > 0 && cfg.RateLimit == 0 {
		cfg.RateLimit = st.RateLimit
	}
	normalizeConfig(cfg)
	return cfg, nil
}

func normalizeConfig(cfg *config.Config) {
	if cfg.Direction == "" {
		cfg.Direction = "both"
	}
	if cfg.Cleanup == "" {
		cfg.Cleanup = "none"
	}
	if cfg.Conflict == "" {
		cfg.Conflict = "newest"
	}
	if cfg.DownloadMode == "" {
		cfg.DownloadMode = "direct"
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 4
	}
	if cfg.Retries <= 0 {
		cfg.Retries = 3
	}
}

// RunTask kicks off a task execution in the background (server lifecycle ctx).
func (r *Runner) RunTask(_ context.Context, id string) error {
	t := r.store.Task(id)
	if t == nil {
		return fmt.Errorf("任务不存在")
	}
	r.mu.Lock()
	if r.running[id] {
		r.mu.Unlock()
		return fmt.Errorf("任务已在运行中")
	}
	r.running[id] = true
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.running, id)
			r.mu.Unlock()
		}()
		r.execute(r.root, t)
	}()
	return nil
}

// execute runs the task with logging, updating status.
func (r *Runner) execute(ctx context.Context, t *Task) {
	conn := r.store.Connection(t.ConnectionID)
	logf := r.logs.LoggerFn(t.Name)
	start := time.Now()
	r.store.UpdateTaskStatus(t.ID, "running", "", "", time.Now())

	if conn == nil {
		errStr := "关联的 OpenList 连接不存在"
		r.store.UpdateTaskStatus(t.ID, "error", errStr, "", time.Now())
		_ = r.store.Save()
		logf("%s", errStr)
		return
	}

	cfg, err := r.buildConfig(t, conn)
	if err != nil {
		r.store.UpdateTaskStatus(t.ID, "error", err.Error(), "", time.Now())
		_ = r.store.Save()
		logf("%v", err)
		return
	}

	c := client.New(cfg.BaseURL, cfg.Token, cfg.Username, cfg.Password, cfg.DownloadMode == "proxy", logf)
	s := syncer.New(cfg, c, logf)
	err = s.Run(ctx)
	dur := time.Since(start).Round(time.Millisecond).String()

	if err != nil {
		logf("任务失败 (%s): %v", dur, err)
		r.store.UpdateTaskStatus(t.ID, "error", err.Error(), "", time.Now())
	} else {
		logf("任务完成 (%s)", dur)
		r.store.UpdateTaskStatus(t.ID, "ok", "", "", time.Now())
	}
	_ = r.store.Save()
}

// parseDurFlex parses Go durations plus a friendly "1d" (days) suffix.
func parseDurFlex(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("无效的天数间隔 %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// scheduleInterval returns the effective interval for a task.
func scheduleInterval(t *Task, def string) time.Duration {
	s := t.Interval
	if s == "" {
		s = def
	}
	d, err := parseDurFlex(s)
	if err != nil || d <= 0 {
		return time.Hour
	}
	return d
}

// ScheduleLoop ticks periodically, running due enabled tasks.
func (r *Runner) ScheduleLoop(ctx context.Context, tick time.Duration, logf func(string, ...any)) {
	if tick <= 0 {
		tick = 30 * time.Second
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st := r.store.Snapshot()
			for _, t := range st.Tasks {
				if ctx.Err() != nil {
					return
				}
				if !t.Enabled || r.IsRunning(t.ID) {
					continue
				}
				iv := scheduleInterval(t, st.Settings.Interval)
				if t.LastRun.IsZero() || time.Since(t.LastRun) >= iv {
					if err := r.RunTask(ctx, t.ID); err != nil {
						logf("调度 %s: %v", t.Name, err)
					}
				}
			}
		}
	}
}