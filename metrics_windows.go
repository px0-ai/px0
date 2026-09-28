//go:build windows

package main

import "time"

// getProcessRusage is a stub on Windows.
func getProcessRusage() (cpuTime time.Duration, peakRSS uint64, ok bool) {
	return 0, 0, false
}
