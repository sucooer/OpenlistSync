package syncer

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"openlist-sync/internal/client"
)

const partSuffix = ".part"

type RemoteEntry struct {
	Path string // full remote path, e.g. /backup/a/b.txt
	Rel  string // slash-separated rel from the task root, e.g. a/b.txt
	Obj  client.FsObject
}

func (r *RemoteEntry) ModTime() (time.Time, bool) { return r.Obj.ModTime() }

type LocalEntry struct {
	Rel   string // slash-separated rel from the task local dir
	IsDir bool
	Size  int64
	MTime time.Time
}

// remoteTree walks the remote directory tree via paginated listings.
func remoteTree(ctx context.Context, c *client.Client, remoteRoot string) (map[string]*RemoteEntry, error) {
	tree := map[string]*RemoteEntry{}
	var walk func(dirRel string) error
	walk = func(dirRel string) error {
		remotePath := remoteRoot
		if dirRel != "" {
			remotePath = path.Join(remoteRoot, dirRel)
		}
		items, err := c.ListAll(ctx, remotePath)
		if err != nil {
			return err
		}
		for _, it := range items {
			if strings.HasSuffix(it.Name, partSuffix) {
				continue
			}
			rel := joinRel(dirRel, it.Name)
			entry := &RemoteEntry{
				Path: path.Join(remoteRoot, rel),
				Rel:  rel,
				Obj:  it,
			}
			tree[rel] = entry
			if it.IsDir {
				if err := walk(rel); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	return tree, nil
}

// localTree walks the local directory tree.
func localTree(dir string) (map[string]*LocalEntry, error) {
	tree := map[string]*LocalEntry{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if strings.HasSuffix(relSlash, partSuffix) {
			return nil // resume temp files never participate in syncing
		}
		le := &LocalEntry{Rel: relSlash, IsDir: d.IsDir()}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			le.Size = info.Size()
			le.MTime = info.ModTime()
		}
		tree[relSlash] = le
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// joinRel joins a parent rel dir with a name using slash separators.
func joinRel(dirRel, name string) string {
	if dirRel == "" {
		return name
	}
	return path.Join(dirRel, name)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}