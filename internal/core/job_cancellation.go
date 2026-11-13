package core

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// requestJobCancellation persists intent independently from replaceable model-job checkpoints.
func (engine *Engine) requestJobCancellation(id string) (any, error) {
	engine.jobsMu.Lock()
	local := engine.jobs[id]
	committing := local != nil && local.committing
	engine.jobsMu.Unlock()
	if committing {
		return map[string]bool{"cancelled": false}, nil
	}
	accepted := false
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		var status string
		err := db.QueryRowContext(background(), "SELECT json_extract(document,'$.status') FROM runs WHERE id=?", id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != "running" {
			return nil
		}
		if _, err := db.ExecContext(background(), "INSERT INTO run_cancel_requests(run_id,requested_at) VALUES(?,?) ON CONFLICT(run_id) DO NOTHING", id, now()); err != nil {
			return err
		}
		accepted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	engine.jobsMu.Lock()
	if job := engine.jobs[id]; accepted && job != nil && !job.committing && job.cancel != nil {
		job.cancel()
	}
	engine.jobsMu.Unlock()
	return map[string]bool{"cancelled": accepted}, nil
}

// watchJobCancellation observes durable requests during streaming calls and exits before the worker closes.
func (engine *Engine) watchJobCancellation(job Job) func() {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-job.ctx.Done():
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(job.ctx, time.Second)
				var requested int
				err := engine.store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_cancel_requests WHERE run_id=?", job.ID).Scan(&requested)
				cancel()
				if err == nil && requested > 0 {
					engine.jobsMu.Lock()
					current := engine.jobs[job.ID]
					committing := current != nil && current.committing
					engine.jobsMu.Unlock()
					if !committing {
						job.cancel()
						return
					}
				}
			}
		}
	}()
	return func() { job.cancel(); <-done }
}
