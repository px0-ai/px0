package main

import (
	"testing"
	"time"
)

func TestMetricsCollection(t *testing.T) {
	// 1. Test RSS measurement
	rss := readProcessRSS()
	if rss == 0 {
		t.Errorf("readProcessRSS() returned 0, expected positive byte count")
	}

	// 2. Test CPU time measurement
	cpuTime, err := readProcessCPUTime()
	if err != nil {
		t.Logf("readProcessCPUTime returned error (expected on unsupported platform): %v", err)
	} else if cpuTime < 0 {
		t.Errorf("readProcessCPUTime() returned negative duration: %v", cpuTime)
	}

	// 3. Test getProcessMetrics
	m := getProcessMetrics(nil)
	if m.RSSBytes == 0 {
		t.Errorf("expected m.RSSBytes > 0, got %d", m.RSSBytes)
	}
	if m.PeakRSSBytes < m.RSSBytes {
		t.Errorf("expected m.PeakRSSBytes (%d) >= m.RSSBytes (%d)", m.PeakRSSBytes, m.RSSBytes)
	}
	if m.Goroutine <= 0 {
		t.Errorf("expected m.Goroutine > 0, got %d", m.Goroutine)
	}

	// 4. Test CPU sampler over a small delay
	usage := globalMetrics.sampleCPU()
	time.Sleep(210 * time.Millisecond)
	usage2 := globalMetrics.sampleCPU()
	if usage < 0 || usage2 < 0 {
		t.Errorf("CPU usage should be non-negative, got %f and %f", usage, usage2)
	}
}
