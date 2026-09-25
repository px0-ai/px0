package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessMetrics captures system resource usage of the px0 server process
// and any child language server processes.
type ProcessMetrics struct {
	RSSBytes    uint64  `json:"rssBytes"`    // Resident set size in bytes
	CPUUsage    float64 `json:"cpuUsage"`    // CPU utilization percentage (e.g. 1.2%)
	Goroutine   int     `json:"goroutines"`  // Current number of active goroutines
	LSPEnabled  bool    `json:"lspEnabled"`  // Whether LSP is active
	LSPMemBytes uint64  `json:"lspMemBytes"` // Combined RSS of running language server child processes
}

// metricsCollector periodically samples process CPU utilization by calculating
// delta CPU time consumed over delta wall-clock time.
type metricsCollector struct {
	mu          sync.Mutex
	lastSample  time.Time
	lastCPUTime time.Duration
	lastUsage   float64
	numCPU      int
}

var globalMetrics = &metricsCollector{
	numCPU: runtime.NumCPU(),
}

// getProcessMetrics samples px0's own process stats, plus the combined
// memory of any running language server processes when lsp is non-nil and
// enabled (-no-lsp turns it off, but the manager itself is never nil).
func getProcessMetrics(lsp *lspManager) ProcessMetrics {
	var m ProcessMetrics
	m.Goroutine = runtime.NumGoroutine()

	// 1. RSS Memory
	m.RSSBytes = readProcessRSS()

	// 2. CPU Usage
	m.CPUUsage = globalMetrics.sampleCPU()

	// 3. Language server memory, if enabled
	if lsp != nil {
		m.LSPEnabled = lsp.Enabled()
		if m.LSPEnabled {
			m.LSPMemBytes = lsp.memBytes()
		}
	}

	return m
}

func readProcessRSS() uint64 {
	// Try Linux /proc/self/statm
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				pageSize := uint64(os.Getpagesize())
				if pageSize == 0 {
					pageSize = 4096
				}
				return pages * pageSize
			}
		}
	}

	// Fallback to runtime.MemStats Sys/HeapAlloc if /proc not present (e.g. Darwin/Windows)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}

// readRSSForPID returns another process's resident set size in bytes.
// Best-effort: 0 if unavailable (process exited, unsupported platform, no
// permission). Used for language server processes, which px0 doesn't own
// the way it owns its own MemStats.
func readRSSForPID(pid int) uint64 {
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid)); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				pageSize := uint64(os.Getpagesize())
				if pageSize == 0 {
					pageSize = 4096
				}
				return pages * pageSize
			}
		}
		return 0
	}

	// Non-Linux (darwin/bsd): no /proc, so shell out to ps, which reports
	// RSS in KB for any process we're allowed to see.
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	kb, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

func (c *metricsCollector) sampleCPU() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cpuTime, err := readProcessCPUTime()
	if err != nil {
		return c.lastUsage
	}

	if c.lastSample.IsZero() {
		c.lastSample = now
		c.lastCPUTime = cpuTime
		return 0.0
	}

	wallDelta := now.Sub(c.lastSample)
	cpuDelta := cpuTime - c.lastCPUTime

	// Only recompute if at least 200ms has elapsed since last sample
	if wallDelta >= 200*time.Millisecond {
		usage := (float64(cpuDelta) / float64(wallDelta)) * 100.0
		if usage < 0 {
			usage = 0
		}
		c.lastUsage = usage
		c.lastSample = now
		c.lastCPUTime = cpuTime
	}

	return c.lastUsage
}

// readProcessCPUTime returns total CPU time consumed by the process (user + system)
func readProcessCPUTime() (time.Duration, error) {
	// Linux /proc/self/stat
	if data, err := os.ReadFile("/proc/self/stat"); err == nil {
		// The comm field is in parentheses and might contain spaces or parentheses: find last ')'
		idx := strings.LastIndex(string(data), ")")
		if idx != -1 && len(data) > idx+2 {
			fields := strings.Fields(string(data[idx+2:]))
			// after comm:
			// state is field 0
			// ppid is field 1
			// pgrp is field 2
			// session is field 3
			// tty_nr is field 4
			// tpgid is field 5
			// flags is field 6
			// minflt is field 7
			// cminflt is field 8
			// majflt is field 9
			// cmajflt is field 10
			// utime is field 11 (offset from field 0)
			// stime is field 12
			if len(fields) >= 13 {
				utime, err1 := strconv.ParseInt(fields[11], 10, 64)
				stime, err2 := strconv.ParseInt(fields[12], 10, 64)
				if err1 == nil && err2 == nil {
					// 100 clock ticks per second on Linux standard (CLK_TCK = 100)
					const clkTck = 100
					totalSec := float64(utime+stime) / float64(clkTck)
					return time.Duration(totalSec * float64(time.Second)), nil
				}
			}
		}
	}

	return 0, fmt.Errorf("cpu time unavailable")
}
