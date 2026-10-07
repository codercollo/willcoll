package jobs

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// A webhook handler starts background work from many requests at once, so Run
// must be safe to call concurrently. Under `go test -race` this fails if Run
// writes any shared state the already-started goroutines read.
func TestRunIsSafeToCallConcurrently(t *testing.T) {
	var g Group
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	var ran atomic.Int32
	var callers sync.WaitGroup
	for range 50 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			g.Run(logger, func() { ran.Add(1) })
		}()
	}
	callers.Wait()
	g.Wait()

	if got := ran.Load(); got != 50 {
		t.Errorf("ran %d jobs, want 50", got)
	}
}

func TestRunRecoversAndLogsAPanic(t *testing.T) {
	var out syncBuffer
	var g Group
	g.Run(slog.New(slog.NewTextHandler(&out, nil)), func() { panic("boom") })
	g.Wait()

	if !strings.Contains(out.String(), "boom") {
		t.Errorf("panic not logged: %q", out.String())
	}
}

func TestRunRecoversAPanicWithNoLogger(t *testing.T) {
	var g Group
	g.Run(nil, func() { panic("boom") }) // must not crash the process
	g.Wait()
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
