package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStableWorkspacePortPersistsAcrossRestarts(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()

	ln1, addr1, err := listenStableWorkspace(root, "127.0.0.1", 7777, false, false)
	if err != nil {
		t.Fatalf("first stable listen failed: %v", err)
	}
	_ = ln1.Close()

	ln2, addr2, err := listenStableWorkspace(root, "127.0.0.1", 7777, false, false)
	if err != nil {
		t.Fatalf("second stable listen failed: %v", err)
	}
	defer ln2.Close()
	if addr2 != addr1 {
		t.Fatalf("stable address changed from %q to %q", addr1, addr2)
	}

	reg, err := readPortRegistry()
	if err != nil {
		t.Fatalf("readPortRegistry failed: %v", err)
	}
	canonical, _ := canonicalWorkspace(root)
	if got := reg.Assignments[canonical]; got != portNumber(addr1) {
		t.Fatalf("registry port = %d, want %d", got, portNumber(addr1))
	}
}

func TestStableWorkspacePortsAreDistinct(t *testing.T) {
	isolateSettings(t)
	root1 := t.TempDir()
	root2 := t.TempDir()

	ln1, addr1, err := listenStableWorkspace(root1, "127.0.0.1", 7777, false, false)
	if err != nil {
		t.Fatalf("first stable listen failed: %v", err)
	}
	defer ln1.Close()
	ln2, addr2, err := listenStableWorkspace(root2, "127.0.0.1", 7777, false, false)
	if err != nil {
		t.Fatalf("second stable listen failed: %v", err)
	}
	defer ln2.Close()
	if addr1 == addr2 {
		t.Fatalf("different workspaces received the same address %q", addr1)
	}
}

func TestStableWorkspacePortConflictIsExplicit(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()
	canonical, _ := canonicalWorkspace(root)
	if err := os.MkdirAll(filepath.Dir(portsPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writePortRegistry(portRegistry{
		Version:     1,
		Assignments: map[string]int{canonical: 7812},
	}); err != nil {
		t.Fatal(err)
	}
	blocked, err := net.Listen("tcp", "127.0.0.1:7812")
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()

	_, _, err = listenStableWorkspace(root, "127.0.0.1", 7777, false, false)
	if err == nil || !strings.Contains(err.Error(), "stable port 7812") {
		t.Fatalf("conflict error = %v, want assigned-port conflict", err)
	}
}

func TestStableWorkspaceExplicitPortDoesNotChangeAssignment(t *testing.T) {
	isolateSettings(t)
	root := t.TempDir()

	ln, addr, err := listenStableWorkspace(root, "127.0.0.1", 0, true, false)
	if err != nil {
		t.Fatalf("explicit stable listen failed: %v", err)
	}
	defer ln.Close()
	if portNumber(addr) <= 0 {
		t.Fatalf("explicit port 0 did not receive an OS-assigned port: %q", addr)
	}
}

func portNumber(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return -1
	}
	n, _ := strconv.Atoi(port)
	return n
}
