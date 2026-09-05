// Package debuglog records opt-in timing evidence without SQL, credentials,
// query results, keystrokes, or raw error messages.
package debuglog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
)

type contextKey struct{}

type recorder struct {
	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
	next    uint64
	err     error
	closed  bool
}

type event struct {
	Time       time.Time `json:"time"`
	Phase      string    `json:"phase"`
	Operation  string    `json:"operation"`
	ID         uint64    `json:"id,omitempty"`
	DurationMS float64   `json:"duration_ms,omitempty"`
	Status     string    `json:"status,omitempty"`
	Version    string    `json:"version,omitempty"`
}

// Open creates a private, new file. Existing paths (including symlinks) are
// rejected. Each event is written immediately so unfinished spans identify
// work still in progress when a process hangs.
func Open(ctx context.Context, path, version string) (context.Context, func() error, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ctx, nil, err
	}
	r := &recorder{file: f, encoder: json.NewEncoder(f)}
	r.write(event{Time: time.Now().UTC(), Phase: "session", Version: version})
	return context.WithValue(ctx, contextKey{}, r), func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.closed = true
		return errors.Join(r.err, r.file.Close())
	}, nil
}

func from(ctx context.Context) *recorder {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(contextKey{}).(*recorder)
	return r
}

func (r *recorder) write(e event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	if err := r.encoder.Encode(e); err != nil && r.err == nil {
		r.err = err
	}
}

// Start measures wall time, including I/O and scheduling. Operation names must
// be code-defined labels, never user input or SQL. It is a no-op when disabled.
func Start(ctx context.Context, operation string) func() {
	r := from(ctx)
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	r.next++
	id := r.next
	r.mu.Unlock()
	r.write(event{Time: time.Now().UTC(), Phase: "start", Operation: operation, ID: id})
	started := time.Now()
	return func() {
		r.write(event{Time: time.Now().UTC(), Phase: "end", Operation: operation, ID: id,
			DurationMS: float64(time.Since(started)) / float64(time.Millisecond)})
	}
}

// Result records only an error category; driver errors can contain secrets.
func Result(ctx context.Context, operation string, err error) {
	r := from(ctx)
	if r == nil {
		return
	}
	status := "ok"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = "timeout"
	case errors.Is(err, context.Canceled):
		status = "canceled"
	case err != nil:
		status = "error"
	}
	r.write(event{Time: time.Now().UTC(), Phase: "result", Operation: operation, Status: status})
}
