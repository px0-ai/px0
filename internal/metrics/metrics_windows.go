//go:build windows

package metrics

import "time"

// GetProcessRusage is a stub on Windows.
func GetProcessRusage() (cpuTime time.Duration, peakRSS uint64, ok bool) {
	return 0, 0, false
}
