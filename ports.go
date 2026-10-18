package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	stablePortStart = 7800
	stablePortEnd   = 7899
	portLockWait    = 5 * time.Second
)

type portRegistry struct {
	Version     int            `json:"version"`
	Assignments map[string]int `json:"assignments"`
}

func portsPath() string {
	p := settingsPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "ports.json")
}

func portLockPath() string {
	p := portsPath()
	if p == "" {
		return ""
	}
	return p + ".lock"
}

type portRegistryLock struct {
	path string
	file *os.File
}

type existingWorkspaceError struct {
	addr string
}

func (e *existingWorkspaceError) Error() string {
	return "workspace is already running at http://" + e.addr
}

func acquirePortRegistryLock() (*portRegistryLock, error) {
	p := portLockPath()
	if p == "" {
		return nil, errors.New("no home directory to save port assignments in")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(portLockWait)
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return &portRegistryLock{path: p, file: f}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for port registry lock %s", p)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (l *portRegistryLock) Close() error {
	if l == nil {
		return nil
	}
	if err := l.file.Close(); err != nil {
		_ = os.Remove(l.path)
		return err
	}
	return os.Remove(l.path)
}

func readPortRegistry() (portRegistry, error) {
	reg := portRegistry{Version: 1, Assignments: make(map[string]int)}
	p := portsPath()
	if p == "" {
		return reg, errors.New("no home directory to read port assignments from")
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return reg, nil
	}
	if err != nil {
		return reg, err
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return reg, fmt.Errorf("invalid port registry %s: %w", p, err)
	}
	if reg.Version == 0 {
		reg.Version = 1
	}
	if reg.Assignments == nil {
		reg.Assignments = make(map[string]int)
	}
	return reg, nil
}

func writePortRegistry(reg portRegistry) error {
	p := portsPath()
	if p == "" {
		return errors.New("no home directory to save port assignments in")
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".ports-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func canonicalWorkspace(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

func listenStableWorkspace(root, host string, requestedPort int, explicitPort, reuse bool) (net.Listener, string, error) {
	if explicitPort && requestedPort == 0 {
		return listen(host, 0)
	}
	canonical, err := canonicalWorkspace(root)
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace for stable port: %w", err)
	}
	lock, err := acquirePortRegistryLock()
	if err != nil {
		return nil, "", err
	}
	defer lock.Close()

	reg, err := readPortRegistry()
	if err != nil {
		return nil, "", err
	}
	assigned, exists := reg.Assignments[canonical]
	if explicitPort {
		assigned = requestedPort
		exists = true
	} else if !exists {
		for p := stablePortStart; p <= stablePortEnd; p++ {
			if portInUse(host, p) {
				continue
			}
			assigned = p
			break
		}
		if assigned == 0 {
			return nil, "", fmt.Errorf("no free stable port available in range %d-%d", stablePortStart, stablePortEnd)
		}
	}
	if assigned == 0 || assigned < 1 || assigned > 65535 {
		return nil, "", fmt.Errorf("invalid stable port %d assigned to %s", assigned, canonical)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(assigned))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if reuse && host == "127.0.0.1" {
			if sameWorkspaceInstance(addr, canonical) {
				return nil, "", &existingWorkspaceError{addr: addr}
			}
		}
		if exists {
			return nil, "", fmt.Errorf("stable port %d for %s is unavailable; use -port N for a one-time override", assigned, canonical)
		}
		return nil, "", fmt.Errorf("stable port %d for %s became unavailable during startup", assigned, canonical)
	}
	if !exists {
		reg.Assignments[canonical] = assigned
		if err := writePortRegistry(reg); err != nil {
			_ = ln.Close()
			return nil, "", err
		}
	}
	return ln, addr, nil
}

func portInUse(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

func sameWorkspaceInstance(addr, root string) bool {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	resp, err := client.Get("http://" + addr + "/api/meta")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var meta struct {
		Root string `json:"root"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return false
	}
	resolved, err := canonicalWorkspace(meta.Root)
	return err == nil && strings.EqualFold(resolved, root)
}
