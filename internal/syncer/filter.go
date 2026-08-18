package syncer

import (
	"path"
	"strings"

	"openlist-sync/internal/client"
)

// Filter decides which files participate in syncing/cleanup.
// Directories always pass; file type filters use the OpenList Type field.
type Filter struct {
	include []string // glob patterns, name must match at least one
	exclude []string // glob patterns, name must match none
	types   map[string]bool
}

func NewFilter(include, exclude, fileTypes []string) *Filter {
	f := &Filter{types: map[string]bool{}}
	f.include = normalizeExt(include)
	f.exclude = normalizeExt(exclude)
	for _, t := range fileTypes {
		f.types[t] = true
	}
	return f
}

// normalizeExt turns the user-supplied extension list into a list of glob
// patterns that match against file basenames via path.Match.
//
// Accepts both bare suffixes ("mp3", ".txt") and full glob patterns
// ("*.mp4", "*foo*"). Bare suffixes are rewritten as "*.<suffix>" so they
// match-by-extension; anything that already looks like a glob is kept as-is.
// Empty entries are dropped.
func normalizeExt(list []string) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, "*?[") {
			out = append(out, p)
			continue
		}
		p = strings.TrimPrefix(p, ".")
		if p == "" {
			continue
		}
		out = append(out, "*."+p)
	}
	return out
}

func (f *Filter) Empty() bool {
	return len(f.include) == 0 && len(f.exclude) == 0 && len(f.types) == 0
}

func (f *Filter) matchName(name string) bool {
	if len(f.include) > 0 {
		ok := false
		for _, p := range f.include {
			if m, _ := path.Match(p, name); m {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, p := range f.exclude {
		if m, _ := path.Match(p, name); m {
			return false
		}
	}
	return true
}

func (f *Filter) matchType(objType int, name string) bool {
	if len(f.types) == 0 {
		return true
	}
	switch objType {
	case 2:
		return f.types["video"]
	case 3:
		return f.types["audio"]
	case 4:
		return f.types["text"]
	case 5:
		return f.types["image"]
	default:
		// unknown type: fall back to a name-based guess
		ext := strings.ToLower(path.Ext(name))
		switch ext {
		case ".mp4", ".mkv", ".avi", ".mov", ".ts", ".m4v", ".webm", ".flv", ".wmv", ".rmvb", ".rm":
			return f.types["video"]
		case ".mp3", ".flac", ".wav", ".aac", ".m4a", ".ogg", ".opus", ".wma":
			return f.types["audio"]
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".svg", ".heic", ".tiff":
			return f.types["image"]
		case ".txt", ".md", ".log", ".json", ".yaml", ".yml", ".xml", ".csv", ".conf", ".ini":
			return f.types["text"]
		}
		return false
	}
}

// Match applies type + name filters to a remote object.
func (f *Filter) Match(o client.FsObject) bool {
	if o.IsDir {
		return true
	}
	return f.matchType(o.Type, o.Name) && f.matchName(o.Name)
}

// MatchLocal applies name filters only (no server type available).
// Extension globs describe file names, so match against the basename rather
// than the slash-separated relative path. Otherwise "*.jpg" would not match
// "album/cover.jpg" because path.Match does not cross '/'.
func (f *Filter) MatchLocal(name string) bool {
	return f.matchName(path.Base(name))
}
