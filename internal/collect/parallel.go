package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maheshrijal/mysq/internal/debuglog"
	"github.com/maheshrijal/mysq/internal/model"
)

const optionalProbeTimeout = 3 * time.Second
const optionalWorkers = 3 // Plus the pinned identity/global-status connection.

var errDifferentServer = errors.New("diagnostic connection reached a different MySQL server or database")

type probeTask struct {
	name string
	run  func(context.Context, *sql.Conn) error
}

type probeWorker struct {
	db   *sql.DB
	conn *sql.Conn
}

func (w *probeWorker) close() {
	if w.conn != nil {
		w.conn.Close()
		w.conn = nil
	}
	if w.db != nil {
		w.db.Close()
		w.db = nil
	}
}

type probePool struct {
	collector *Collector
	target    Target
	server    model.Server
	workers   [optionalWorkers]probeWorker
}

func (p *probePool) close() {
	for i := range p.workers {
		p.workers[i].close()
	}
}

// run joins every task before returning. Each task owns its output fields;
// capability/warning slices must only be appended by the caller after the join.
func (p *probePool) run(ctx context.Context, tasks []probeTask) ([]error, error) {
	errs := runProbeTasks(ctx, tasks, len(p.workers), optionalProbeTimeout, func(ctx context.Context, worker int, task probeTask) error {
		w := &p.workers[worker]
		if w.conn == nil {
			var err error
			w.db, w.conn, err = p.collector.openConnectionWithLimit(ctx, p.target, optionalProbeTimeout)
			if err != nil {
				return err
			}
			var server model.Server
			if err := p.collector.collectServer(ctx, w.conn, &server); err != nil {
				w.close()
				return err
			}
			if server.UUID != p.server.UUID || server.Database != p.server.Database {
				w.close()
				return errDifferentServer
			}
		}
		err := task.run(ctx, w.conn)
		// A driver cancellation invalidates this pinned connection. Any replacement
		// must receive session safeguards and identity validation again.
		if ctx.Err() != nil {
			w.close()
			return ctx.Err()
		}
		return err
	})
	if ctx.Err() != nil {
		return errs, ctx.Err()
	}
	for _, err := range errs {
		if errors.Is(err, errDifferentServer) {
			return errs, err
		}
	}
	return errs, nil
}

// Keeping scheduling independent of SQL permits deterministic concurrency and
// cancellation regression tests without issuing slow queries to a real server.
func runProbeTasks(ctx context.Context, tasks []probeTask, workers int, timeout time.Duration,
	run func(context.Context, int, probeTask) error) []error {
	errs := make([]error, len(tasks))
	jobs := make(chan int, len(tasks))
	for i := range tasks {
		jobs <- i
	}
	close(jobs)
	var wg sync.WaitGroup
	for worker := 0; worker < min(workers, len(tasks)); worker++ {
		wg.Go(func() {
			for i := range jobs {
				if ctx.Err() != nil {
					errs[i] = ctx.Err()
					continue
				}
				probeCtx, cancel := context.WithTimeout(ctx, timeout)
				done := debuglog.Start(probeCtx, "probe."+tasks[i].name)
				err := run(probeCtx, worker, tasks[i])
				if probeCtx.Err() != nil {
					err = fmt.Errorf("probe exceeded budget or was canceled: %w", probeCtx.Err())
				}
				debuglog.Result(probeCtx, "probe."+tasks[i].name, err)
				done()
				cancel()
				errs[i] = err
			}
		})
	}
	wg.Wait()
	return errs
}
