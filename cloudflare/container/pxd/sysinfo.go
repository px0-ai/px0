package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

// memAvailableMB reports memory available to THIS container specifically.
// /proc/meminfo is deliberately NOT used here: inside a container it
// reflects the host/VM's memory, not the container's own cgroup limit —
// confirmed empirically (a container capped at 300MB via `docker run
// --memory` still reports several GB of host MemAvailable). That would
// make eviction never trigger in practice, since the host always looks
// like it has plenty of room even as this specific container approaches
// its real, much smaller limit. Cgroup v2's memory.max/memory.current
// (falling back to v1's equivalents) give the number that's actually
// true for this container; /proc/meminfo is only a last-resort fallback
// for non-containerized local dev, and a fail-open default after that.
func memAvailableMB() int {
	if mb, ok := cgroupV2MemAvailableMB(); ok {
		return mb
	}
	if mb, ok := cgroupV1MemAvailableMB(); ok {
		return mb
	}
	if mb, ok := procMeminfoAvailableMB(); ok {
		return mb
	}
	return 1 << 20
}

func cgroupV2MemAvailableMB() (int, bool) {
	max, ok := readCgroupInt("/sys/fs/cgroup/memory.max")
	if !ok || max <= 0 { // "max" (unlimited) reads as a parse failure here — treat as no limit
		return 0, false
	}
	cur, ok := readCgroupInt("/sys/fs/cgroup/memory.current")
	if !ok {
		return 0, false
	}
	return int((max - cur) / (1024 * 1024)), true
}

func cgroupV1MemAvailableMB() (int, bool) {
	max, ok := readCgroupInt("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	if !ok || max <= 0 || max > 1<<60 { // v1 uses a huge sentinel for "unlimited"
		return 0, false
	}
	cur, ok := readCgroupInt("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	if !ok {
		return 0, false
	}
	return int((max - cur) / (1024 * 1024)), true
}

func readCgroupInt(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func procMeminfoAvailableMB() (int, bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			break
		}
		return kb / 1024, true
	}
	return 0, false
}

// diskFreeMB reports free space on the filesystem containing path. Fails
// open for the same reason as memAvailableMB.
func diskFreeMB(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 1 << 40
	}
	return int64(stat.Bavail) * int64(stat.Bsize) / (1024 * 1024)
}
