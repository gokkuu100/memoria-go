// Package jobs runs in-process scheduled work (SYSTEM_DESIGN §7). Every job
// takes a pg advisory lock first, so running a second API instance is safe.
package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// Job is a named unit of scheduled work. LockKey must be unique per job —
// it scopes the advisory lock so distinct jobs never serialize each other.
type Job struct {
	Name         string
	Spec         string // cron spec, e.g. "@hourly"
	LockKey      int64
	RunAtStartup bool
	Run          func(ctx context.Context) error
}

type Runner struct {
	pool    *pgxpool.Pool
	cron    *cron.Cron
	startup []Job
}

func NewRunner(pool *pgxpool.Pool) *Runner {
	return &Runner{pool: pool, cron: cron.New()}
}

// Register schedules a job. Call before Start.
func (r *Runner) Register(j Job) error {
	if _, err := r.cron.AddFunc(j.Spec, func() { r.execute(context.Background(), j) }); err != nil {
		return err
	}
	if j.RunAtStartup {
		r.startup = append(r.startup, j)
	}
	return nil
}

// Start launches the scheduler and fires startup jobs in the background.
func (r *Runner) Start(ctx context.Context) {
	r.cron.Start()
	go func() {
		for _, j := range r.startup {
			r.execute(ctx, j)
		}
	}()
}

func (r *Runner) Stop() {
	<-r.cron.Stop().Done()
}

// execute runs the job under its advisory lock; if another instance holds the
// lock the run is skipped (the other instance is doing the same work).
func (r *Runner) execute(ctx context.Context, j Job) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		slog.Error("job: acquiring conn", "job", j.Name, "error", err)
		return
	}
	defer conn.Release()

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", j.LockKey).Scan(&locked); err != nil {
		slog.Error("job: taking advisory lock", "job", j.Name, "error", err)
		return
	}
	if !locked {
		slog.Debug("job: skipped, lock held elsewhere", "job", j.Name)
		return
	}
	defer func() {
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", j.LockKey); err != nil {
			slog.Error("job: releasing advisory lock", "job", j.Name, "error", err)
		}
	}()

	start := time.Now()
	if err := j.Run(ctx); err != nil {
		slog.Error("job: failed", "job", j.Name, "error", err)
		return
	}
	slog.Info("job: done", "job", j.Name, "took", time.Since(start).Round(time.Millisecond))
}
