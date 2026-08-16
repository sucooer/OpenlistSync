package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Task describes one remote->local mapping.
type Task struct {
	RemotePath string
	LocalDir   string
}

type Config struct {
	BaseURL  string
	Token    string
	Username string
	Password string

	Tasks []Task

	Direction    string // both | pull | push
	Cleanup      string // none | local | remote | both
	Conflict     string // newest | remote | local | skip
	IncludeExt   []string
	ExcludeExt   []string
	FileTypes    []string // video | audio | image | text
	Concurrency  int
	RateLimit    int64 // bytes/second, 0 = unlimited
	Retries      int
	DownloadMode string // direct | proxy
	Interval     time.Duration
	HealthAddr   string
	DryRun       bool
	Verbose      bool
	Version      bool
}

// ErrHelp is returned when the user asked for usage output.
var ErrHelp = flag.ErrHelp

func usage(fs *flag.FlagSet) {
	fmt.Fprintf(fs.Output(), `openlist-sync - OpenList bidirectional sync tool

Usage:
  openlist-sync [sync] [flags]       run a sync pass once
  openlist-sync daemon [flags]       run periodically until interrupted
  openlist-sync version              print version

Every flag can be set via an environment variable (flags win), see README.

Flags:
`)
	fs.PrintDefaults()
}

// Parse merges flags and environment variables (flag wins, then env, then default).
func Parse(sub string, args []string) (*Config, error) {
	cfg := &Config{}
	fs := flag.NewFlagSet("openlist-sync "+sub, flag.ContinueOnError)
	fs.Usage = func() { usage(fs) }

	var (
		flBaseURL, flToken, flUser, flPass string
		flRemote, flLocal, flTasksFile     string
		flDirection, flCleanup, flConflict string
		flInclude, flExclude, flTypes      string
		flConcurrency                      int
		flRateLimit                        int64
		flRetries                          int
		flMode, flInterval, flHealth       string
		flDryRun, flVerbose, flVersion     bool
	)

	fs.StringVar(&flBaseURL, "base-url", "", "OpenList base URL")
	fs.StringVar(&flToken, "token", "", "OpenList API token (raw Authorization header)")
	fs.StringVar(&flUser, "username", "", "OpenList username (used with --password to login)")
	fs.StringVar(&flPass, "password", "", "OpenList password")
	fs.StringVar(&flRemote, "remote-path", "", "OpenList path to sync, e.g. /movies")
	fs.StringVar(&flLocal, "local-dir", "", "local directory to sync with")
	fs.StringVar(&flTasksFile, "tasks-file", "", "file with task lines: remotePath|localDir")
	fs.StringVar(&flDirection, "direction", "", "sync direction: both|pull|push (default both)")
	fs.StringVar(&flCleanup, "cleanup", "", "delete files missing on the other side: none|local|remote|both (default none)")
	fs.StringVar(&flConflict, "conflict", "", "conflict policy: newest|remote|local|skip (default newest)")
	fs.StringVar(&flInclude, "include-ext", "", "include only files matching these glob patterns, comma separated, e.g. *.mp4,*.mkv")
	fs.StringVar(&flExclude, "exclude-ext", "", "skip files matching these glob patterns, comma separated")
	fs.StringVar(&flTypes, "type", "", "sync only these file types, comma separated: video,audio,image,text")
	fs.IntVar(&flConcurrency, "concurrency", 0, "parallel download/upload workers (default 4)")
	fs.Int64Var(&flRateLimit, "rate-limit", 0, "global transfer rate limit in bytes/sec (0 = unlimited)")
	fs.IntVar(&flRetries, "retries", 0, "per-file retries on failure (default 3)")
	fs.StringVar(&flMode, "download-mode", "", "download mode: direct|proxy (default direct)")
	fs.StringVar(&flInterval, "interval", "", "daemon sync interval, e.g. 30m, 6h (default 1h)")
	fs.StringVar(&flHealth, "health-addr", "", "daemon health HTTP listen address, e.g. :8080 (empty = disabled)")
	fs.BoolVar(&flDryRun, "dry-run", false, "show what would be done without doing it")
	fs.BoolVar(&flVerbose, "verbose", false, "verbose logging")
	fs.BoolVar(&flVersion, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.BaseURL = pick(fs, "base-url", flBaseURL, "OPENLIST_BASE_URL")
	cfg.Token = pick(fs, "token", flToken, "OPENLIST_TOKEN")
	cfg.Username = pick(fs, "username", flUser, "OPENLIST_USERNAME")
	cfg.Password = pick(fs, "password", flPass, "OPENLIST_PASSWORD")
	cfg.Direction = pick(fs, "direction", flDirection, "SYNC_DIRECTION")
	cfg.Cleanup = pick(fs, "cleanup", flCleanup, "SYNC_CLEANUP")
	cfg.Conflict = pick(fs, "conflict", flConflict, "SYNC_CONFLICT")
	cfg.DownloadMode = pick(fs, "download-mode", flMode, "SYNC_DOWNLOAD_MODE")
	cfg.HealthAddr = pick(fs, "health-addr", flHealth, "SYNC_HEALTH_ADDR")

	cfg.IncludeExt = splitList(pick(fs, "include-ext", flInclude, "SYNC_INCLUDE_EXT"))
	cfg.ExcludeExt = splitList(pick(fs, "exclude-ext", flExclude, "SYNC_EXCLUDE_EXT"))
	cfg.FileTypes = splitList(pick(fs, "type", flTypes, "SYNC_TYPE"))

	if v, err := pickInt(fs, "concurrency", flConcurrency, "SYNC_CONCURRENCY", 4); err != nil {
		return nil, err
	} else {
		cfg.Concurrency = v
	}
	if v, err := pickInt64(fs, "rate-limit", flRateLimit, "SYNC_RATE_LIMIT", 0); err != nil {
		return nil, err
	} else {
		cfg.RateLimit = v
	}
	if v, err := pickInt(fs, "retries", flRetries, "SYNC_RETRIES", 3); err != nil {
		return nil, err
	} else {
		cfg.Retries = v
	}
	if v, err := pickDuration(fs, "interval", flInterval, "SYNC_INTERVAL", time.Hour); err != nil {
		return nil, err
	} else {
		cfg.Interval = v
	}

	cfg.DryRun = flDryRun || envBool("SYNC_DRY_RUN")
	cfg.Verbose = flVerbose || envBool("SYNC_VERBOSE")
	cfg.Version = flVersion

	// Tasks: OPENLIST_TASKS env lines + tasks file, then fallback to single task.
	remote := pick(fs, "remote-path", flRemote, "OPENLIST_REMOTE_PATH")
	local := pick(fs, "local-dir", flLocal, "OPENLIST_LOCAL_DIR")
	tasks := parseTaskLines(os.Getenv("OPENLIST_TASKS"))
	if tf := pick(fs, "tasks-file", flTasksFile, "SYNC_TASKS_FILE"); tf != "" {
		lines, err := readTaskLines(tf)
		if err != nil {
			return nil, fmt.Errorf("tasks-file: %w", err)
		}
		tasks = append(tasks, lines...)
	}
	if len(tasks) == 0 {
		if remote == "" || local == "" {
			return nil, fmt.Errorf("no tasks: set --remote-path/--local-dir, OPENLIST_TASKS or --tasks-file")
		}
		tasks = []Task{{RemotePath: remote, LocalDir: local}}
	}
	cfg.Tasks = tasks

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.normalize()
	return cfg, nil
}

// normalize fills implicit defaults so downstream logic always sees concrete values.
func (c *Config) normalize() {
	if c.Direction == "" {
		c.Direction = "both"
	}
	if c.Cleanup == "" {
		c.Cleanup = "none"
	}
	if c.Conflict == "" {
		c.Conflict = "newest"
	}
	if c.DownloadMode == "" {
		c.DownloadMode = "direct"
	}
}

func (c *Config) validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("missing OPENLIST_BASE_URL / --base-url")
	}
	if c.Token == "" && (c.Username == "" || c.Password == "") {
		return fmt.Errorf("need a token or username+password")
	}
	switch c.Direction {
	case "", "both", "pull", "push":
	default:
		return fmt.Errorf("invalid --direction %q", c.Direction)
	}
	switch c.Cleanup {
	case "", "none", "local", "remote", "both":
	default:
		return fmt.Errorf("invalid --cleanup %q", c.Cleanup)
	}
	switch c.Conflict {
	case "", "newest", "remote", "local", "skip":
	default:
		return fmt.Errorf("invalid --conflict %q", c.Conflict)
	}
	switch c.DownloadMode {
	case "", "direct", "proxy":
	default:
		return fmt.Errorf("invalid --download-mode %q", c.DownloadMode)
	}
	for _, t := range c.FileTypes {
		switch t {
		case "video", "audio", "image", "text":
		default:
			return fmt.Errorf("invalid --type %q (want video,audio,image,text)", t)
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseTaskLines(s string) []Task {
	var tasks []Task
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if t, ok := parseTaskLine(line); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func parseTaskLine(line string) (Task, bool) {
	parts := strings.SplitN(line, "|", 2)
	if len(parts) != 2 {
		return Task{}, false
	}
	r := strings.TrimSpace(parts[0])
	l := strings.TrimSpace(parts[1])
	if r == "" || l == "" {
		return Task{}, false
	}
	if !strings.HasPrefix(r, "/") {
		r = "/" + r
	}
	return Task{RemotePath: r, LocalDir: l}, true
}

func readTaskLines(path string) ([]Task, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tasks []Task
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if t, ok := parseTaskLine(line); ok {
			tasks = append(tasks, t)
		}
	}
	return tasks, sc.Err()
}

// --- env+flag helpers ---

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func pick(fs *flag.FlagSet, name, flagVal, envKey string) string {
	if isSet(fs, name) {
		return flagVal
	}
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return flagVal
}

func pickInt(fs *flag.FlagSet, name string, flagVal int, envKey string, def int) (int, error) {
	if isSet(fs, name) {
		return flagVal, nil
	}
	if v := os.Getenv(envKey); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", envKey, err)
		}
		return n, nil
	}
	if flagVal != 0 {
		return flagVal, nil
	}
	return def, nil
}

func pickInt64(fs *flag.FlagSet, name string, flagVal int64, envKey string, def int64) (int64, error) {
	if isSet(fs, name) {
		return flagVal, nil
	}
	if v := os.Getenv(envKey); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", envKey, err)
		}
		return n, nil
	}
	if flagVal != 0 {
		return flagVal, nil
	}
	return def, nil
}

func pickDuration(fs *flag.FlagSet, name string, flagVal string, envKey string, def time.Duration) (time.Duration, error) {
	v := pick(fs, name, flagVal, envKey)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("--%s: %w", name, err)
	}
	return d, nil
}

func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}