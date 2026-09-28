//go:build !windows

package main

import (
	"runtime"
	"syscall"
	"time"
)

// getProcessRusage reads user+system CPU time and peak resident set size
// using the POSIX getrusage syscall available on Darwin, Linux, and BSDs.
func getProcessRusage() (cpuTime time.Duration, peakRSS uint64, ok bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, false
	}
	utime := time.Duration(ru.Utime.Sec)*time.Second + time.Duration(ru.Utime.Usec)*time.Microsecond
	stime := time.Duration(ru.Stime.Sec)*time.Second + time.Duration(ru.Stime.Usec)*time.Microsecond

	// On Darwin/macOS, ru.Maxrss is reported in bytes.
	// On Linux, FreeBSD, OpenBSD, and NetBSD, ru.Maxrss is reported in kilobytes.
	maxRSS := uint64(ru.Maxrss)
	if runtime.GOOS != "darwin" && runtime.GOOS != "ios" {
		maxRSS *= 1024
	}
	return utime + stime, maxRSS, true
}
