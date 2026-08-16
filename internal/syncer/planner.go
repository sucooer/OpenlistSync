package syncer

import (
	"path"
	"sort"
	"strings"
	"time"
)

type JobKind string

const (
	JobMkdirLocal  JobKind = "mkdir_local"
	JobMkdirRemote JobKind = "mkdir_remote"
	JobDownload    JobKind = "download"
	JobUpload      JobKind = "upload"
	JobRmLocalFile JobKind = "rm_local_file"
	JobRmLocalDir  JobKind = "rm_local_dir"
	JobRmRemote    JobKind = "rm_remote"
)

type Job struct {
	Kind      JobKind
	Rel       string
	Remote    *RemoteEntry
	Local     *LocalEntry
	Overwrite bool // upload: target exists remotely
	Parent    string
	Names     []string
}

type Plan struct {
	Jobs []Job
	// stats
	MkdirLocal  int
	MkdirRemote int
	Download    int
	Upload      int
	RmLocal     int
	RmRemote    int
}

const mtimeTolerance = time.Second

type planner struct {
	direction string // both | pull | push
	cleanup   string // none | local | remote | both
	conflict  string // newest | remote | local | skip
	filter    *Filter
}

func (p *planner) wantsPull() bool {
	return p.direction == "both" || p.direction == "pull"
}

func (p *planner) wantsPush() bool {
	return p.direction == "both" || p.direction == "push"
}

func (p *planner) cleanupLocal() bool {
	return p.cleanup == "local" || p.cleanup == "both"
}

func (p *planner) cleanupRemote() bool {
	return p.cleanup == "remote" || p.cleanup == "both"
}

func (p *planner) plan(remote map[string]*RemoteEntry, local map[string]*LocalEntry) *Plan {
	plan := &Plan{}
	if p.wantsPull() {
		p.planPull(remote, local, plan)
	}
	if p.wantsPush() {
		p.planPush(remote, local, plan)
	}
	// Cleanup must not fight the transfer phases: a file being uploaded this
	// pass is not "missing" locally, and one being downloaded is not "missing"
	// remotely. Compute the transfer sets first, then clean around them.
	uploading := map[string]bool{}
	downloading := map[string]bool{}
	for _, j := range plan.Jobs {
		switch j.Kind {
		case JobUpload:
			uploading[j.Rel] = true
		case JobDownload:
			downloading[j.Rel] = true
		}
	}
	if p.cleanupLocal() {
		p.planCleanupLocal(remote, local, plan, uploading)
	}
	if p.cleanupRemote() {
		p.planCleanupRemote(remote, local, plan, downloading)
	}
	return plan
}

func (p *planner) planPull(remote map[string]*RemoteEntry, local map[string]*LocalEntry, plan *Plan) {
	for _, rel := range sortedKeys(remote) {
		re := remote[rel]
		le, hasLocal := local[rel]
		if re.Obj.IsDir {
			if !hasLocal || !le.IsDir {
				plan.Jobs = append(plan.Jobs, Job{Kind: JobMkdirLocal, Rel: rel})
				plan.MkdirLocal++
			}
			continue
		}
		if !p.filter.Match(re.Obj) {
			continue
		}
		if !hasLocal || le.IsDir {
			plan.Jobs = append(plan.Jobs, Job{Kind: JobDownload, Rel: rel, Remote: re})
			plan.Download++
			continue
		}
		rm, rmOK := re.ModTime()
		if le.Size == re.Obj.Size && rmOK && timesEq(le.MTime, rm) {
			continue // unchanged
		}
		// remote newer -> refresh local
		if rmOK && rm.After(le.MTime.Add(mtimeTolerance)) {
			plan.Jobs = append(plan.Jobs, Job{Kind: JobDownload, Rel: rel, Remote: re})
			plan.Download++
			continue
		}
		// local newer -> conflict per policy
		if rmOK && le.MTime.After(rm.Add(mtimeTolerance)) {
			if p.conflict == "remote" {
				plan.Jobs = append(plan.Jobs, Job{Kind: JobDownload, Rel: rel, Remote: re})
				plan.Download++
			}
			continue
		}
		// timestamps tie but size differs -> direction side wins
		plan.Jobs = append(plan.Jobs, Job{Kind: JobDownload, Rel: rel, Remote: re})
		plan.Download++
	}
}

func (p *planner) planPush(remote map[string]*RemoteEntry, local map[string]*LocalEntry, plan *Plan) {
	for _, rel := range sortedKeys(local) {
		le := local[rel]
		re, hasRemote := remote[rel]
		if le.IsDir {
			if !hasRemote || !re.Obj.IsDir {
				plan.Jobs = append(plan.Jobs, Job{Kind: JobMkdirRemote, Rel: rel})
				plan.MkdirRemote++
			}
			continue
		}
		if !p.filter.MatchLocal(rel) {
			continue
		}
		overwrite := false
		if !hasRemote {
			plan.Jobs = append(plan.Jobs, Job{Kind: JobUpload, Rel: rel, Local: le})
			plan.Upload++
			continue
		}
		rm, rmOK := re.ModTime()
		if le.Size == re.Obj.Size && rmOK && timesEq(le.MTime, rm) {
			continue // unchanged
		}
		// local newer -> upload
		if !rmOK {
			if le.Size == re.Obj.Size {
				continue
			}
			overwrite = true
		} else if le.MTime.After(rm.Add(mtimeTolerance)) {
			overwrite = true
		} else if !rm.After(le.MTime.Add(mtimeTolerance)) {
			// tie with differing size -> direction side wins
			overwrite = true
		} else {
			// remote newer -> conflict per policy
			if p.conflict != "local" {
				continue
			}
			overwrite = true
		}
		plan.Jobs = append(plan.Jobs, Job{Kind: JobUpload, Rel: rel, Local: le, Overwrite: overwrite})
		plan.Upload++
	}
}

func (p *planner) planCleanupLocal(remote map[string]*RemoteEntry, local map[string]*LocalEntry, plan *Plan, uploading map[string]bool) {
	var dirs []string
	for _, rel := range sortedKeys(local) {
		le := local[rel]
		if _, ok := remote[rel]; ok {
			continue
		}
		if uploading[rel] {
			continue // will be pushed this pass
		}
		if le.IsDir {
			dirs = append(dirs, rel)
			continue
		}
		if !p.filter.MatchLocal(rel) {
			continue
		}
		plan.Jobs = append(plan.Jobs, Job{Kind: JobRmLocalFile, Rel: rel, Local: le})
		plan.RmLocal++
	}
	// deepest dirs first so emptied parents get removed
	sort.Slice(dirs, func(i, j int) bool { return depth(dirs[i]) > depth(dirs[j]) })
	for _, rel := range dirs {
		plan.Jobs = append(plan.Jobs, Job{Kind: JobRmLocalDir, Rel: rel, Local: local[rel]})
		plan.RmLocal++
	}
}

func (p *planner) planCleanupRemote(remote map[string]*RemoteEntry, local map[string]*LocalEntry, plan *Plan, downloading map[string]bool) {
	batch := map[string][]string{}
	for _, rel := range sortedKeys(remote) {
		re := remote[rel]
		if re.Obj.IsDir {
			continue
		}
		if _, ok := local[rel]; ok {
			continue
		}
		if downloading[rel] {
			continue // will be pulled this pass
		}
		if !p.filter.Match(re.Obj) {
			continue
		}
		batch[path.Dir(re.Path)] = append(batch[path.Dir(re.Path)], re.Obj.Name)
	}
	for _, dir := range sortedKeys(batch) {
		plan.Jobs = append(plan.Jobs, Job{Kind: JobRmRemote, Parent: dir, Names: batch[dir]})
		plan.RmRemote += len(batch[dir])
	}
}

func timesEq(a, b time.Time) bool {
	d := a.Sub(b)
	return d <= mtimeTolerance && d >= -mtimeTolerance
}

func depth(rel string) int { return strings.Count(rel, "/") }