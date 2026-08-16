package syncer

import (
	"testing"
	"time"

	"openlist-sync/internal/client"
)

func mod(t time.Time) string { return t.Format(time.RFC3339Nano) }

func remoteFile(rel string, size int64, m string) map[string]*RemoteEntry {
	return map[string]*RemoteEntry{
		rel: {Rel: rel, Obj: client.FsObject{Name: rel, Size: size, Modified: m}},
	}
}

func localFile(rel string, size int64, m time.Time) map[string]*LocalEntry {
	return map[string]*LocalEntry{
		rel: {Rel: rel, Size: size, MTime: m},
	}
}

func TestPullNewDownloads(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "pull", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remoteFile("a.txt", 10, mod(now)), nil)
	if plan.Download != 1 || plan.Jobs[0].Kind != JobDownload {
		t.Fatalf("expected 1 download, got %+v", plan)
	}
}

func TestPullUnchangedSkipped(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "pull", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remoteFile("a.txt", 10, mod(now)), localFile("a.txt", 10, now))
	if plan.Download != 0 || len(plan.Jobs) != 0 {
		t.Fatalf("expected no-op, got %+v", plan)
	}
}

func TestPullRemoteNewerDownloads(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "pull", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remoteFile("a.txt", 10, mod(now)), localFile("a.txt", 12, now.Add(-time.Hour)))
	if plan.Download != 1 {
		t.Fatalf("expected download, got %+v", plan)
	}
}

func TestPullLocalNewerConflict(t *testing.T) {
	now := time.Now()
	newest := &planner{direction: "pull", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	remoteWins := &planner{direction: "pull", cleanup: "none", conflict: "remote", filter: NewFilter(nil, nil, nil)}
	skip := &planner{direction: "pull", cleanup: "none", conflict: "skip", filter: NewFilter(nil, nil, nil)}

	local := localFile("a.txt", 10, now.Add(+time.Hour))
	remote := remoteFile("a.txt", 10, mod(now))

	if p := newest.plan(remote, local); p.Download != 0 {
		t.Fatalf("newest: local is newer, expected skip, got %+v", p)
	}
	if p := remoteWins.plan(remote, local); p.Download != 1 {
		t.Fatalf("remote policy: expected download, got %+v", p)
	}
	if p := skip.plan(remote, local); p.Download != 0 {
		t.Fatalf("skip policy: expected no-op, got %+v", p)
	}
}

func TestPullTieSizesDiffer(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "pull", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remoteFile("a.txt", 20, mod(now)), localFile("a.txt", 10, now))
	if plan.Download != 1 {
		t.Fatalf("tie with different size should download, got %+v", plan)
	}
}

func TestPushNewUploads(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "push", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(nil, localFile("a.txt", 10, now))
	if plan.Upload != 1 || plan.Jobs[0].Overwrite {
		t.Fatalf("expected upload without overwrite, got %+v", plan)
	}
}

func TestPushLocalNewerOverwrites(t *testing.T) {
	now := time.Now()
	p := &planner{direction: "push", cleanup: "none", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remoteFile("a.txt", 5, mod(now.Add(-time.Hour))), localFile("a.txt", 15, now))
	if plan.Upload != 1 || !plan.Jobs[0].Overwrite {
		t.Fatalf("expected overwrite upload, got %+v", plan)
	}
}

func TestCleanupLocalRespectsFilter(t *testing.T) {
	now := time.Now()
	remote := remoteFile("a.txt", 10, mod(now))
	local := localFile("a.txt", 10, now)
	local["junk.tmp"] = &LocalEntry{Rel: "junk.tmp", Size: 1, MTime: now}

	p := &planner{direction: "pull", cleanup: "local", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remote, local)
	if plan.RmLocal != 1 || plan.Jobs[0].Rel != "junk.tmp" {
		t.Fatalf("expected junk.tmp removed only, got %+v", plan)
	}

	excl := &planner{direction: "pull", cleanup: "local", conflict: "newest", filter: NewFilter(nil, []string{"*.tmp"}, nil)}
	if plan := excl.plan(remote, local); plan.RmLocal != 0 {
		t.Fatalf("excluded file should not be removed, got %+v", plan)
	}
}

func TestCleanupRemoteBatchesByDir(t *testing.T) {
	now := time.Now()
	remote := map[string]*RemoteEntry{}
	remote["gone1.txt"] = &RemoteEntry{Path: "/r/gone1.txt", Rel: "gone1.txt", Obj: client.FsObject{Name: "gone1.txt", Size: 1, Modified: mod(now)}}
	remote["sub/gone2.txt"] = &RemoteEntry{Path: "/r/sub/gone2.txt", Rel: "sub/gone2.txt", Obj: client.FsObject{Name: "gone2.txt", Size: 1, Modified: mod(now)}}

	p := &planner{direction: "push", cleanup: "remote", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(remote, nil)
	if plan.RmRemote != 2 || len(plan.Jobs) != 2 {
		t.Fatalf("expected 2 remote removals in 2 batches, got %+v", plan)
	}
	for _, j := range plan.Jobs {
		if j.Kind != JobRmRemote || len(j.Names) != 1 {
			t.Fatalf("bad removal job: %+v", j)
		}
	}
}

func TestCleanupDoesNotFightTransfers(t *testing.T) {
	now := time.Now()
	// local-only file with cleanup=both: it should be uploaded, not deleted
	p := &planner{direction: "both", cleanup: "both", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan := p.plan(nil, localFile("mine.txt", 10, now))
	if plan.Upload != 1 || plan.RmLocal != 0 {
		t.Fatalf("local-only file should upload, not be cleaned: %+v", plan)
	}

	// remote-only file with cleanup=both: it should be downloaded, not removed remotely
	p = &planner{direction: "both", cleanup: "both", conflict: "newest", filter: NewFilter(nil, nil, nil)}
	plan = p.plan(remoteFile("theirs.txt", 10, mod(now)), nil)
	if plan.Download != 1 || plan.RmRemote != 0 {
		t.Fatalf("remote-only file should download, not be cleaned: %+v", plan)
	}
}

func TestTypeFilter(t *testing.T) {
	f := NewFilter(nil, nil, []string{"video"})
	if !f.Match(client.FsObject{Name: "x.mp4", Type: 2}) {
		t.Fatal("video should match")
	}
	if f.Match(client.FsObject{Name: "x.jpg", Type: 5}) {
		t.Fatal("image should not match video filter")
	}
	if f.Match(client.FsObject{Name: "x.txt", Type: 4}) {
		t.Fatal("text should not match video filter")
	}
}