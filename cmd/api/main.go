package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // user timezones must resolve inside the distroless image

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/albums"
	"memoria-backend/internal/analytics"
	"memoria-backend/internal/capsules"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/exports"
	"memoria-backend/internal/jobs"
	"memoria-backend/internal/mailer"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/sentryx"
	"memoria-backend/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	sentryFlush, _ := sentryx.Init()
	defer sentryFlush()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	setupLogger(cfg.Env)

	pool, err := pg.NewPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return fmt.Errorf("creating db pool: %w", err)
	}
	defer pool.Close()

	if err := waitForDB(ctx, pool); err != nil {
		return err
	}
	if err := migrate(pool); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	var m mailer.Mailer
	if cfg.SMTPHost != "" {
		m = &mailer.SMTP{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		}
		slog.Info("mailer: smtp", "host", cfg.SMTPHost, "from", cfg.SMTPFrom)
	} else {
		m = mailer.Log{}
		slog.Info("mailer: dev log mode (set SMTP_HOST to send real email)")
	}

	store, err := media.NewStore(ctx, cfg)
	if err != nil {
		return err
	}
	if err := store.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensuring media bucket: %w", err)
	}

	q := dbgen.New(pool)
	expo := notifications.NewExpoClient(cfg.ExpoPushURL, cfg.ExpoAccessToken)
	outbox := notifications.NewOutboxService(pool, q, expo)
	outbox.Start(ctx)
	defer outbox.Stop()

	tracker := analytics.NewTracker(ctx, pool)
	analytics.SetDefault(tracker)
	defer tracker.Stop()

	exportWorker := &exports.Worker{
		Pool: pool, Q: q, Store: store,
		Notifier: &exports.OutboxNotifier{Svc: outbox},
	}

	runner := jobs.NewRunner(pool)
	if err := runner.Register(jobs.Job{
		Name:         "media_orphan_sweep",
		Spec:         "@hourly",
		LockKey:      1001,
		RunAtStartup: true,
		Run: func(ctx context.Context) error {
			return media.SweepOrphans(ctx, q, store)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "expire_invites",
		Spec:    "*/15 * * * *",
		LockKey: 1002,
		Run: func(ctx context.Context) error {
			if err := albums.ExpireInvites(ctx, q, outbox); err != nil {
				return err
			}
			return capsules.ExpireInvites(ctx, q, outbox)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "album_lifespan",
		Spec:    "@hourly",
		LockKey: 1003,
		Run: func(ctx context.Context) error {
			return albums.RunLifespan(ctx, q)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:         "archive_sweep",
		Spec:         "@daily",
		LockKey:      1004,
		RunAtStartup: false,
		Run: func(ctx context.Context) error {
			if err := albums.ArchiveSweep(ctx, q, store); err != nil {
				return err
			}
			return capsules.RunArchiveSweep(ctx, q)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "freeze_warning",
		Spec:    "@hourly",
		LockKey: 1005,
		Run: func(ctx context.Context) error {
			return capsules.RunFreezeWarning(ctx, q, outbox)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "freeze_capsules",
		Spec:    "@hourly",
		LockKey: 1006,
		Run: func(ctx context.Context) error {
			return capsules.RunFreeze(ctx, q, outbox)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "unlock_capsules",
		Spec:    "*/5 * * * *",
		LockKey: 1007,
		Run: func(ctx context.Context) error {
			return capsules.RunUnlock(ctx, q, outbox)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "unlock_reminder",
		Spec:    "* * * * *",
		LockKey: 1010,
		Run: func(ctx context.Context) error {
			return capsules.RunUnlockReminder(ctx, q, outbox)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "flush_notification_batches",
		Spec:    "*/5 * * * *",
		LockKey: 1008,
		Run: func(ctx context.Context) error {
			outbox.FlushBatches(ctx)
			return nil
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	if err := runner.Register(jobs.Job{
		Name:    "process_exports",
		Spec:    "*/1 * * * *",
		LockKey: 1009,
		Run: func(ctx context.Context) error {
			return exportWorker.RunPending(ctx)
		},
	}); err != nil {
		return fmt.Errorf("registering jobs: %w", err)
	}
	runner.Start(ctx)
	defer runner.Stop()

		srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: server.New(pool, cfg, m, store, &server.Deps{
			Notify:       outbox,
			Notif:        &notifications.Handler{Q: q, Flusher: outbox},
			ExportWorker: exportWorker,
			Analytics: &analytics.Handler{
				Pool:       pool,
				Tracker:    tracker,
				MetricsKey: cfg.MetricsKey,
			},
		}).Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "port", cfg.Port, "env", cfg.Env)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func setupLogger(env config.Env) {
	var handler slog.Handler
	if env == config.EnvProduction {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(handler))
}

// waitForDB retries pings so the container survives postgres starting slower
// than the API under docker compose.
func waitForDB(ctx context.Context, pool *pgxpool.Pool) error {
	const attempts = 10
	for i := 1; i <= attempts; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := pool.Ping(pingCtx)
		cancel()
		if err == nil {
			return nil
		}
		slog.Warn("database not ready", "attempt", i, "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i) * 500 * time.Millisecond):
		}
	}
	return fmt.Errorf("database unreachable after %d attempts", attempts)
}

func migrate(pool *pgxpool.Pool) error {
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if err := sqlDB.Close(); err != nil {
			slog.Warn("closing migration db handle", "error", err)
		}
	}()
	return goose.Up(sqlDB, "migrations")
}
