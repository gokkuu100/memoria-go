package analytics

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"memoria-backend/internal/pg"
)

const (
	queueSize   = 4096
	flushSize   = 64
	flushEvery  = 500 * time.Millisecond
	insertTO    = 3 * time.Second
)

// Event is one analytics row ready for persistence.
type Event struct {
	UserID     *uuid.UUID
	Name       string
	Properties map[string]any
	OccurredAt time.Time
	Source     string
}

// Tracker buffers events and writes them asynchronously so handlers never block.
type Tracker struct {
	pool   *pgxpool.Pool
	ch     chan Event
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

var (
	defaultMu sync.RWMutex
	defaultT  *Tracker
)

// SetDefault registers the process-wide tracker used by Track().
func SetDefault(t *Tracker) {
	defaultMu.Lock()
	defaultT = t
	defaultMu.Unlock()
}

// Track enqueues an event. Never blocks; drops when the queue is full.
func Track(userID *uuid.UUID, name string, props map[string]any) {
	defaultMu.RLock()
	t := defaultT
	defaultMu.RUnlock()
	if t == nil {
		return
	}
	t.Enqueue(Event{
		UserID:     userID,
		Name:       name,
		Properties: SanitizeProps(props),
		OccurredAt: time.Now().UTC(),
		Source:     SourceServer,
	})
}

// TrackUser is a convenience for a known user id.
func TrackUser(userID uuid.UUID, name string, props map[string]any) {
	uid := userID
	Track(&uid, name, props)
}

// NewTracker starts a background flusher. Call Stop on shutdown.
func NewTracker(parent context.Context, pool *pgxpool.Pool) *Tracker {
	ctx, cancel := context.WithCancel(parent)
	t := &Tracker{
		pool:   pool,
		ch:     make(chan Event, queueSize),
		cancel: cancel,
	}
	t.wg.Add(1)
	go t.loop(ctx)
	return t
}

// Enqueue is non-blocking. Returns false if dropped.
func (t *Tracker) Enqueue(e Event) bool {
	if t == nil {
		return false
	}
	if e.Name == "" {
		return false
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.Source == "" {
		e.Source = SourceServer
	}
	e.Properties = SanitizeProps(e.Properties)
	select {
	case t.ch <- e:
		return true
	default:
		slog.Warn("analytics: queue full, dropping event", "event", e.Name)
		return false
	}
}

// Stop drains and stops the worker.
func (t *Tracker) Stop() {
	if t == nil {
		return
	}
	t.cancel()
	t.wg.Wait()
}

func (t *Tracker) loop(ctx context.Context) {
	defer t.wg.Done()
	buf := make([]Event, 0, flushSize)
	ticker := time.NewTicker(flushEvery)
	defer ticker.Stop()

	flush := func() {
		if len(buf) == 0 {
			return
		}
		batch := buf
		buf = make([]Event, 0, flushSize)
		t.writeBatch(batch)
	}

	for {
		select {
		case <-ctx.Done():
			// Drain remaining without blocking forever.
			for {
				select {
				case e := <-t.ch:
					buf = append(buf, e)
					if len(buf) >= flushSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case e := <-t.ch:
			buf = append(buf, e)
			if len(buf) >= flushSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (t *Tracker) writeBatch(batch []Event) {
	ctx, cancel := context.WithTimeout(context.Background(), insertTO)
	defer cancel()

	tx, err := t.pool.Begin(ctx)
	if err != nil {
		slog.Warn("analytics: begin tx", "error", err, "n", len(batch))
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const q = `
INSERT INTO analytics_events (id, user_id, event_name, properties, occurred_at, source)
VALUES ($1, $2, $3, $4, $5, $6)`

	for _, e := range batch {
		props, err := json.Marshal(e.Properties)
		if err != nil {
			props = []byte("{}")
		}
		var uid any
		if e.UserID != nil {
			uid = pg.UUID(*e.UserID)
		}
		if _, err := tx.Exec(ctx, q,
			pg.UUID(uuid.New()),
			uid,
			e.Name,
			props,
			e.OccurredAt,
			e.Source,
		); err != nil {
			slog.Warn("analytics: insert failed", "event", e.Name, "error", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		slog.Warn("analytics: commit failed", "error", err, "n", len(batch))
	}
}

// FlushForTest blocks until the queue is empty and a flush has run (tests only).
func (t *Tracker) FlushForTest() {
	if t == nil {
		return
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(t.ch) == 0 {
			time.Sleep(flushEvery + 50*time.Millisecond)
			if len(t.ch) == 0 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}
