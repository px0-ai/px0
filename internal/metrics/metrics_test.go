package metrics

import (
	"testing"
	"time"
)

func TestMetricsCollection(t *testing.T) {
	// 1. Test RSS measurement
	rss := ReadProcessRSS()
	if rss == 0 {
		t.Errorf("ReadProcessRSS() returned 0, expected positive byte count")
	}

	// 2. Test CPU time measurement
	cpuTime, err := ReadProcessCPUTime()
	if err != nil {
		t.Logf("ReadProcessCPUTime returned error (expected on unsupported platform): %v", err)
	} else if cpuTime < 0 {
		t.Errorf("ReadProcessCPUTime() returned negative duration: %v", cpuTime)
	}

	// 3. Test Collect
	m := Collect(0, false)
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
	usage := DefaultCollector.SampleCPU()
	time.Sleep(210 * time.Millisecond)
	usage2 := DefaultCollector.SampleCPU()
	if usage < 0 || usage2 < 0 {
		t.Errorf("CPU usage should be non-negative, got %f and %f", usage, usage2)
	}
}
