package metrics

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
	RSSBytes     uint64  `json:"rssBytes"`     // Resident set size in bytes
	PeakRSSBytes uint64  `json:"peakRSSBytes"` // Peak resident set size in bytes
	CPUUsage     float64 `json:"cpuUsage"`     // CPU utilization percentage (e.g. 1.2%)
	Goroutine    int     `json:"goroutines"`   // Current number of active goroutines
	LSPEnabled   bool    `json:"lspEnabled"`   // Whether LSP is active
	LSPMemBytes  uint64  `json:"lspMemBytes"`  // Combined RSS of running language server child processes
}

// Collector periodically samples process CPU utilization by calculating
// delta CPU time consumed over delta wall-clock time.
type Collector struct {
	mu          sync.Mutex
	lastSample  time.Time
	lastCPUTime time.Duration
	lastUsage   float64
	numCPU      int
}

// DefaultCollector is the shared system-wide metrics collector.
var DefaultCollector = &Collector{
	numCPU: runtime.NumCPU(),
}

// Collect returns a snapshot of process metrics, incorporating the given LSP usage.
func Collect(lspMem uint64, lspEnabled bool) ProcessMetrics {
	var m ProcessMetrics
	m.Goroutine = runtime.NumGoroutine()

	// 1. RSS Memory and Peak RSS
	m.RSSBytes = ReadProcessRSS()
	if _, peak, ok := GetProcessRusage(); ok && peak > 0 {
		m.PeakRSSBytes = peak
	}
	if m.PeakRSSBytes < m.RSSBytes {
		m.PeakRSSBytes = m.RSSBytes
	}

	// 2. CPU Usage
	m.CPUUsage = DefaultCollector.SampleCPU()

	// 3. Language server memory
	m.LSPEnabled = lspEnabled
	m.LSPMemBytes = lspMem

	return m
}

// ReadProcessRSS returns resident set size of the current process in bytes.
func ReadProcessRSS() uint64 {
	// 1. Try Linux /proc/self/statm
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

	// 2. Try Darwin/BSD via ps for self PID
	if rss := ReadRSSForPID(os.Getpid()); rss > 0 {
		return rss
	}

	// 3. Fallback to runtime.MemStats Sys/HeapAlloc if /proc and ps not present (e.g. Windows)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.Sys
}

// ReadRSSForPID returns another process's resident set size in bytes.
func ReadRSSForPID(pid int) uint64 {
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

	// Non-Linux (darwin/bsd): shell out to ps
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

// SampleCPU samples process CPU utilization by calculating delta CPU time consumed over delta wall-clock time.
func (c *Collector) SampleCPU() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	cpuTime, err := ReadProcessCPUTime()
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

// ReadProcessCPUTime returns total CPU time consumed by the process (user + system).
func ReadProcessCPUTime() (time.Duration, error) {
	if cpuTime, _, ok := GetProcessRusage(); ok {
		return cpuTime, nil
	}

	// Linux /proc/self/stat fallback
	if data, err := os.ReadFile("/proc/self/stat"); err == nil {
		idx := strings.LastIndex(string(data), ")")
		if idx != -1 && len(data) > idx+2 {
			fields := strings.Fields(string(data[idx+2:]))
			if len(fields) >= 13 {
				utime, err1 := strconv.ParseInt(fields[11], 10, 64)
				stime, err2 := strconv.ParseInt(fields[12], 10, 64)
				if err1 == nil && err2 == nil {
					const clkTck = 100
					totalSec := float64(utime+stime) / float64(clkTck)
					return time.Duration(totalSec * float64(time.Second)), nil
				}
			}
		}
	}

	return 0, fmt.Errorf("cpu time unavailable")
}
