// pxd is the in-container supervisor for px0-cf's shared multi-repo mode:
// it spawns one px0 process per resident repo, reverse-proxies to whichever
// one a request is for, and evicts the LRU-oldest inactive repo under disk
// or memory pressure. See cloudflare/README.md (or the plan doc) for the
// full design.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

const (
	proxyPort  = "7777"
	statusPort = "8081"
)

func main() {
	reg := newRegistry()

	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/status/", statusHandler(reg))
		log.Printf("status server listening on :%s", statusPort)
		log.Fatal(http.ListenAndServe(":"+statusPort, mux))
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", proxyHandler(reg))
	log.Printf("proxy server listening on :%s", proxyPort)
	log.Fatal(http.ListenAndServe(":"+proxyPort, mux))
}

// statusHandler serves GET /status/<owner>/<repo>.json?ref=<ref>. It is
// side-effecting: if the repo isn't resident yet, this call is what
// triggers getOrSpawn. PxContainer.ts polls this in a loop until the
// response says ready/error, then forwards the real request to :7777.
func statusHandler(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner, repo, ok := parseStatusPath(r.URL.Path)
		if !ok {
			http.Error(w, `{"error":"bad status path"}`, http.StatusBadRequest)
			return
		}
		ref := r.URL.Query().Get("ref")
		if ref == "" {
			ref = "HEAD"
		}

		rr := reg.getOrSpawn(owner, repo, ref)
		status, message := rr.getStatus()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": status, "message": message})
	}
}

func parseStatusPath(p string) (owner, repo string, ok bool) {
	p = strings.TrimPrefix(p, "/status/")
	p = strings.TrimSuffix(p, ".json")
	parts := strings.SplitN(p, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// proxyHandler serves :7777. By the time a request reaches here,
// PxContainer.ts has already polled the repo to "ready" via statusHandler
// — this just looks it up and reverse-proxies. If it's missing (evicted in
// the narrow race between the status poll and this request, or the repo
// was never provisioned) it returns a clear error rather than silently
// spawning again on a random port.
func proxyHandler(reg *registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owner := r.Header.Get("X-Px0-Owner")
		repo := r.Header.Get("X-Px0-Repo")
		ref := r.Header.Get("X-Px0-Ref")
		if ref == "" {
			ref = "HEAD"
		}
		if owner == "" || repo == "" {
			http.Error(w, `{"error":"missing repo context"}`, http.StatusBadRequest)
			return
		}

		reg.mu.Lock()
		rr, ok := reg.repos[owner+"/"+repo]
		reg.mu.Unlock()
		if !ok || rr.ref != ref {
			http.Error(w, `{"error":"repo not resident — retry the status poll"}`, http.StatusConflict)
			return
		}
		status, message := rr.getStatus()
		if status != "ready" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": status, "message": message})
			return
		}

		rr.touch()
		target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(rr.port)}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ServeHTTP(w, r)
	}
}
