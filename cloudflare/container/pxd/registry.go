package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const (
	reposRoot     = "/repos"
	basePort      = 8100
	spawnTimeout  = 20 * time.Second
	errorCooldown = 15 * time.Second // how long a failed attempt is cached before allowing a retry
)

// Budget knobs, overridable via env vars so eviction behavior can be tested
// locally without actually exhausting a real container's disk/memory (see
// build-order step 2 in the plan). Defaults match the `lite` instance type
// (256MiB RAM, 2GB disk); memPerProcMB is set from real measurement (~25MB
// observed per resident px0 process locally, not the ~15MB first guessed).
var (
	maxRepoMB     = envInt("PX0_MAX_REPO_MB", 200)
	memReserveMB  = envInt("PXD_MEM_RESERVE_MB", 64) // OS + supervisor baseline
	memPerProcMB  = envInt("PXD_MEM_PER_PROC_MB", 25)
	minFreeDiskMB = envInt("PXD_MIN_FREE_DISK_MB", 150)
	activeWindow  = time.Duration(envInt("PXD_ACTIVE_WINDOW_SEC", 10)) * time.Second
)

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitize(s string) string {
	return safeName.ReplaceAllString(s, "_")
}

type residentRepo struct {
	owner, repo, ref string
	dir              string
	port             int
	cmd              *exec.Cmd

	mu         sync.Mutex
	status     string // booting, checking-size, cloning, ready, error
	message    string
	statusAt   time.Time // when status last changed — used to time out a cached error
	lastAccess time.Time
	createdAt  time.Time
	done       chan struct{} // closed once provisioning finishes (ready or error)
}

func (r *residentRepo) setStatus(status, message string) {
	r.mu.Lock()
	r.status, r.message, r.statusAt = status, message, time.Now()
	r.mu.Unlock()
	log.Printf("[%s/%s@%s] %s %s", r.owner, r.repo, r.ref, status, message)
}

func (r *residentRepo) getStatus() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.message
}

func (r *residentRepo) touch() {
	r.mu.Lock()
	r.lastAccess = time.Now()
	r.mu.Unlock()
}

func (r *residentRepo) lastAccessTime() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastAccess
}

func (r *residentRepo) statusSince() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusAt
}

func (r *residentRepo) kill() {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
		_, _ = r.cmd.Process.Wait()
	}
	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
	}
}

type registry struct {
	mu       sync.Mutex
	repos    map[string]*residentRepo // key: owner/repo
	nextPort int
}

func newRegistry() *registry {
	return &registry{repos: map[string]*residentRepo{}, nextPort: basePort}
}

func (reg *registry) allocPort() int {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	p := reg.nextPort
	reg.nextPort++
	return p
}

// getOrSpawn returns the resident repo for owner/repo@ref, starting
// provisioning if it isn't already resident (or is resident with a
// different ref, in which case the old one is evicted first). Provisioning
// happens synchronously up to the point of registry insertion, then
// continues in the background; callers wait on repo.done for completion.
func (reg *registry) getOrSpawn(owner, repo, ref string) *residentRepo {
	key := owner + "/" + repo

	reg.mu.Lock()
	if rr, ok := reg.repos[key]; ok {
		if rr.ref == ref {
			status, _ := rr.getStatus()
			if status == "error" && time.Since(rr.statusSince()) < errorCooldown {
				// Cached failure, still within cooldown: return it as-is
				// rather than re-attempting. Without this, a client that
				// keeps polling a broken/nonexistent repo (or one that
				// simply never learns to stop polling on error) drives a
				// tight retry loop against GitHub's API and git fetch on
				// every single poll.
				reg.mu.Unlock()
				return rr
			}
			if status != "error" {
				reg.mu.Unlock()
				rr.touch()
				return rr
			}
			// Cooldown elapsed on a previously-failed attempt: fall
			// through and retry fresh.
			delete(reg.repos, key)
			reg.mu.Unlock()
			reg.mu.Lock()
		} else {
			// Ref switch on an already-resident repo: evict the old
			// checkout first (only one ref per repo is ever "hot" at a time).
			delete(reg.repos, key)
			reg.mu.Unlock()
			rr.kill()
			reg.mu.Lock()
		}
	}

	rr := &residentRepo{
		owner: owner, repo: repo, ref: ref,
		status: "booting", createdAt: time.Now(), lastAccess: time.Now(),
		done: make(chan struct{}),
	}
	reg.repos[key] = rr
	reg.mu.Unlock()

	go reg.provision(rr)
	return rr
}

func (reg *registry) remove(rr *residentRepo) {
	reg.mu.Lock()
	key := rr.owner + "/" + rr.repo
	if cur, ok := reg.repos[key]; ok && cur == rr {
		delete(reg.repos, key)
	}
	reg.mu.Unlock()
}

// provision runs a repo's full startup sequence. On any failure it marks
// the repo "error" and cleans up its process/disk resources via kill(),
// but deliberately does NOT remove it from the registry — getOrSpawn
// caches that error for errorCooldown so a client that keeps polling a
// broken repo doesn't drive a tight retry loop against GitHub's API and
// git fetch on every single poll.
func (reg *registry) provision(rr *residentRepo) {
	defer close(rr.done)

	rr.setStatus("checking-size", "")
	if sizeKB, err := githubRepoSizeKB(rr.owner, rr.repo); err == nil && sizeKB > maxRepoMB*1024 {
		rr.setStatus("error", fmt.Sprintf("repo exceeds %dMB cap", maxRepoMB))
		return
	}

	rr.setStatus("cloning", "")
	if err := reg.ensureRoom(rr); err != nil {
		rr.setStatus("error", err.Error())
		return
	}

	dir := filepath.Join(reposRoot, sanitize(rr.owner), sanitize(rr.repo))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rr.setStatus("error", "mkdir failed: "+err.Error())
		return
	}
	rr.dir = dir

	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", rr.owner, rr.repo)
	if err := runGit(dir, "init", "-q"); err != nil {
		rr.setStatus("error", "git init failed: "+err.Error())
		rr.kill()
		return
	}
	if err := runGit(dir, "remote", "add", "origin", cloneURL); err != nil {
		rr.setStatus("error", "git remote add failed: "+err.Error())
		rr.kill()
		return
	}

	// Start px0 BEFORE the fetch completes: px0 memoizes git-availability
	// once per process the first time it's asked, so .git must already
	// exist (it does — git init above) before px0's first request, or it
	// caches "no git" forever. See git.go / gitProbe() in upstream px0.
	port := reg.allocPort()
	rr.port = port
	cmd := exec.Command("px0", "-host", "127.0.0.1", "-port", strconv.Itoa(port),
		"-no-open", "-no-telemetry", "-no-agent", dir)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		rr.setStatus("error", "failed to start px0: "+err.Error())
		rr.kill()
		return
	}
	rr.cmd = cmd

	if err := runGit(dir, "fetch", "--depth=1", "origin", rr.ref); err != nil {
		rr.setStatus("error", "fetch failed: "+err.Error())
		rr.kill()
		return
	}
	if err := runGit(dir, "checkout", "-q", "FETCH_HEAD"); err != nil {
		rr.setStatus("error", "checkout failed: "+err.Error())
		rr.kill()
		return
	}

	if !waitForPort(port, spawnTimeout) {
		rr.setStatus("error", "px0 did not become ready in time")
		rr.kill()
		return
	}

	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/api/reindex", port), "", nil)
	if err == nil {
		resp.Body.Close()
	}

	rr.setStatus("ready", "")
}

// ensureRoom evicts LRU-oldest, currently-inactive resident repos (never
// touching the repo being provisioned, and never a repo accessed within
// activeWindow) until there's enough free disk AND enough available memory
// for one more resident repo. Returns an error if nothing more can be
// evicted and the budget still isn't met.
func (reg *registry) ensureRoom(forRepo *residentRepo) error {
	for {
		diskOK := diskFreeMB(reposRoot) > int64(minFreeDiskMB)
		memOK := memAvailableMB() > memReserveMB+memPerProcMB
		if diskOK && memOK {
			return nil
		}

		victim := reg.pickEvictable(forRepo)
		if victim == nil {
			return fmt.Errorf("at capacity: no evictable repo and insufficient %s",
				map[bool]string{true: "disk", false: "memory"}[!diskOK])
		}
		log.Printf("[%s/%s@%s] evicted (LRU, last used %s ago) to make room for %s/%s",
			victim.owner, victim.repo, victim.ref, time.Since(victim.lastAccessTime()).Round(time.Second),
			forRepo.owner, forRepo.repo)
		reg.remove(victim)
		victim.kill()
	}
}

func (reg *registry) pickEvictable(exclude *residentRepo) *residentRepo {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	var best *residentRepo
	for _, rr := range reg.repos {
		if rr == exclude {
			continue
		}
		if status, _ := rr.getStatus(); status != "ready" {
			continue // never evict something still mid-provision
		}
		if time.Since(rr.lastAccessTime()) < activeWindow {
			continue // actively in use — never evict
		}
		if best == nil || rr.lastAccessTime().Before(best.lastAccessTime()) {
			best = rr
		}
	}
	return best
}

func githubRepoSizeKB(owner, repo string) (int, error) {
	resp, err := http.Get(fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var payload struct {
		Size int `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	return payload.Size, nil
}
