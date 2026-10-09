package main

// Benchmarks for the LSP client hot paths. These use only APIs that exist
// before and after the fix, so the same file measures both sides.
//
// Quick before/after comparison:
//	go test -run=NONE -bench=BenchmarkLSP -benchmem -count=3 .
//
// Full detail (allocations, longer samples, CPU + memory profiles):
//	go test -run=NONE -bench=BenchmarkLSP -benchmem -benchtime=2s -count=3 \
//	    -cpuprofile=cpu.out -memprofile=mem.out .
//	go tool pprof -top -nodecount=25 cpu.out
//	go tool pprof -top -nodecount=25 -alloc_space mem.out
//
//   - BenchmarkLSPToLSP / BenchmarkLSPFromLSP: position encoding conversion
//     across ascii/latin/cjk/emoji lines and utf-8/utf-16/utf-32 encodings.
//   - BenchmarkLSPToLSPLongLine: same, on a ~12KB mixed line (scaling).
//   - BenchmarkLSPReadFrame: Content-Length frame parsing at 0.5KB/8KB/64KB.
//   - BenchmarkLSPReadFrameManyHeaders: 25 headers isolate the header parser.
//   - BenchmarkLSPNotify: marshal + frame + write against a draining pipe.
//   - BenchmarkLSPNotifyParallel: same from 8 goroutines (lock contention:
//     one big mutex vs. the dedicated writer's channel).
//   - BenchmarkLSPEnsureOpen: open a fresh file per iteration (real didOpen).
//   - BenchmarkLSPSyncDocChanged: 100KB doc, new content every iteration.
//   - BenchmarkLSPSyncDocUnchanged: 100KB doc, identical content (the fix
//     skips the resend via a content hash; the old code resends everything).
//   - BenchmarkLSPBinDirs: language-server binary discovery (env walk + npm
//     probe + PATH scans); the fix memoizes this.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var lspBenchLines = map[string]string{
	"ascii": "func main() { fmt.Println(\"hello, world\") } // trailing comment here",
	"latin": "héllo → wörld: naïve café résumé",
	"cjk":   "func 你好世界() { fmt.Println(\"日本語テスト\") } // コメント",
	"emoji": "🎉🚀✨ " + strings.Repeat("x🎉", 40),
}

// lspBenchLongLine is a ~12KB mixed-encoding line for scaling measurements.
var lspBenchLongLine = strings.Repeat("héllo → wörld 🎉 x = 42; ", 500)

func BenchmarkLSPToLSP(b *testing.B) {
	for name, line := range lspBenchLines {
		for _, enc := range []string{"utf-8", "utf-16", "utf-32"} {
			b.Run(name+"/"+enc, func(b *testing.B) {
				cl := &lspClient{encoding: enc}
				col := len(line) / 2
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = cl.toLSP(line, 7, col)
				}
			})
		}
	}
}

func BenchmarkLSPFromLSP(b *testing.B) {
	for name, line := range lspBenchLines {
		for _, enc := range []string{"utf-8", "utf-16", "utf-32"} {
			b.Run(name+"/"+enc, func(b *testing.B) {
				cl := &lspClient{encoding: enc}
				// Convert at the same column toLSP produced, so the work is comparable.
				pos := cl.toLSP(line, 7, len(line)/2)
				lines := []string{line}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = cl.fromLSP(lines, pos)
				}
			})
		}
	}
}

func BenchmarkLSPToLSPLongLine(b *testing.B) {
	for _, enc := range []string{"utf-8", "utf-16", "utf-32"} {
		b.Run(enc, func(b *testing.B) {
			cl := &lspClient{encoding: enc}
			col := len(lspBenchLongLine) * 3 / 4
			b.ReportAllocs()
			b.SetBytes(int64(len(lspBenchLongLine)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = cl.toLSP(lspBenchLongLine, 100, col)
			}
		})
	}
}

func BenchmarkLSPReadFrame(b *testing.B) {
	for _, size := range []int{512, 8192, 65536} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			payload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0", "id": 1, "method": "textDocument/didOpen",
				"params": map[string]any{"pad": strings.Repeat("x", size)},
			})
			if err != nil {
				b.Fatal(err)
			}
			var frame bytes.Buffer
			fmt.Fprintf(&frame, "Content-Length: %d\r\n\r\n", len(payload))
			frame.Write(payload)
			data := frame.Bytes()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := bufio.NewReader(bytes.NewReader(data))
				if _, err := readFrame(r); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLSPReadFrameManyHeaders(b *testing.B) {
	var hdr bytes.Buffer
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&hdr, "X-Custom-Header-%d: some-value-%d\r\n", i, i)
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "m"})
	if err != nil {
		b.Fatal(err)
	}
	fmt.Fprintf(&hdr, "Content-Length: %d\r\n\r\n", len(payload))
	hdr.Write(payload)
	data := hdr.Bytes()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bufio.NewReader(bytes.NewReader(data))
		if _, err := readFrame(r); err != nil {
			b.Fatal(err)
		}
	}
}

// lspBenchDrainClient wires a client to a pipe whose server side is drained.
func lspBenchDrainClient(b *testing.B) *lspClient {
	b.Helper()
	cl := newLSPClient(lspServerDef{Name: "fake", Cmd: []string{"fake"}}, b.TempDir())
	srvR, srvW := io.Pipe()
	cl.in = srvW
	b.Cleanup(func() { srvR.Close(); srvW.Close() })
	go func() {
		br := bufio.NewReader(srvR)
		for {
			if _, err := readFrame(br); err != nil {
				return
			}
		}
	}()
	if starter, ok := any(cl).(interface{ beginWriteLoop() }); ok {
		starter.beginWriteLoop()
	}
	return cl
}

func BenchmarkLSPNotify(b *testing.B) {
	cl := lspBenchDrainClient(b)
	params := map[string]any{
		"textDocument": map[string]any{"uri": "file:///f.go", "version": 1},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cl.notify("textDocument/didChange", params); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLSPNotifyParallel(b *testing.B) {
	cl := lspBenchDrainClient(b)
	params := map[string]any{
		"textDocument": map[string]any{"uri": "file:///f.go", "version": 1},
	}
	var failed atomic.Bool
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if cl.notify("textDocument/didChange", params) != nil {
				failed.Store(true)
				return
			}
		}
	})
	if failed.Load() {
		b.Fatal("notify failed under parallel load")
	}
}

func BenchmarkLSPEnsureOpen(b *testing.B) {
	cl, _ := lspRaceWireClient(b)
	dir := b.TempDir()
	content := []byte("package main\n\nfunc main() {}\n")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		if err := os.WriteFile(path, content, 0644); err != nil {
			b.Fatal(err)
		}
		if err := cl.ensureOpen(path, "f.go"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLSPSyncDocChanged(b *testing.B) {
	cl, _ := lspRaceWireClient(b)
	path := filepath.Join(b.TempDir(), "f.go")
	content := []byte(strings.Repeat("x", 100*1024)) // 100KB doc: IPC cost is visible
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		b.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Mutate the tail so every iteration is a genuine content change.
		for j := 0; j < 8; j++ {
			content[len(content)-1-j] = byte('0' + (i+j)%10)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			b.Fatal(err)
		}
		if err := cl.syncDoc(path, "f.go"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLSPSyncDocUnchanged(b *testing.B) {
	cl, _ := lspRaceWireClient(b)
	path := filepath.Join(b.TempDir(), "f.go")
	content := []byte(strings.Repeat("x", 100*1024)) // 100KB doc: IPC cost is visible
	if err := os.WriteFile(path, content, 0644); err != nil {
		b.Fatal(err)
	}
	if err := cl.ensureOpen(path, "f.go"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cl.syncDoc(path, "f.go"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLSPBinDirs(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lspBinDirs()
	}
}

func BenchmarkLSPServerStatusBinDirs(b *testing.B) {
	// ServerStatus re-resolves install state on every call: uncached
	// lspBinDirs() before the fix (env walk + npm probe + PATH scans),
	// memoized cachedBinDirs() after. This benchmark measures the
	// optimization; BenchmarkLSPBinDirs above is the uncached control.
	m := newLSPManager(b.TempDir(), false)
	def := &lspServerDef{Name: "bench", Cmd: []string{"go"}, Exts: []string{".go"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.ServerStatus(def)
	}
}
