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
	f.include = include
	f.exclude = exclude
	for _, t := range fileTypes {
		f.types[t] = true
	}
	return f
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
func (f *Filter) MatchLocal(name string) bool {
	return f.matchName(name)
}