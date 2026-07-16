package analytics_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/analytics"
	"memoria-backend/internal/auth"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/server"
)

type env struct {
	ts      *httptest.Server
	pool    *pgxpool.Pool
	q       *dbgen.Queries
	tracker *analytics.Tracker
	key     string
}

var (
	setupOnce sync.Once
	testEnv   *env
	setupErr  error
)

type logMailer struct{}

func (logMailer) Send(context.Context, string, string, string) error { return nil }

func getEnv(t *testing.T) *env {
	t.Helper()
	setupOnce.Do(func() {
		adminURL := os.Getenv("TEST_ADMIN_DATABASE_URL")
		if adminURL == "" {
			adminURL = "postgres://memoria:memoria@localhost:5432/memoria?sslmode=disable"
		}
		admin, err := sql.Open("pgx", adminURL)
		if err != nil {
			setupErr = err
			return
		}
		defer func() { _ = admin.Close() }()
		if err := admin.Ping(); err != nil {
			setupErr = fmt.Errorf("postgres unreachable (run `make up`): %w", err)
			return
		}
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_analytics_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_analytics_test"); err != nil {
			setupErr = err
			return
		}

		dbURL := "postgres://memoria:memoria@localhost:5432/memoria_analytics_test?sslmode=disable"
		mig, err := sql.Open("pgx", dbURL)
		if err != nil {
			setupErr = err
			return
		}
		goose.SetBaseFS(db.Migrations)
		goose.SetLogger(goose.NopLogger())
		if err := goose.SetDialect("postgres"); err != nil {
			setupErr = err
			_ = mig.Close()
			return
		}
		if err := goose.Up(mig, "migrations"); err != nil {
			setupErr = err
			_ = mig.Close()
			return
		}
		_ = mig.Close()

		pool, err := pgxpool.New(context.Background(), dbURL)
		if err != nil {
			setupErr = err
			return
		}

		cfg := &config.Config{
			Env:                     config.EnvDevelopment,
			JWTSecret:               "test-jwt-secret",
			S3Endpoint:              "http://localhost:9000",
			S3PublicEndpoint:        "http://localhost:9000",
			S3AccessKey:             "memoria",
			S3SecretKey:             "memoria-secret",
			S3Bucket:                "memoria-analytics-test",
			S3UsePathStyle:          true,
			MetricsKey:              "test-metrics-key",
			RevenueCatWebhookSecret: "dev",
		}
		store, err := media.NewStore(context.Background(), cfg)
		if err != nil {
			setupErr = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.EnsureBucket(ctx); err != nil {
			setupErr = fmt.Errorf("minio unreachable (run `make up`): %w", err)
			return
		}

		tracker := analytics.NewTracker(context.Background(), pool)
		analytics.SetDefault(tracker)
		key := "test-metrics-key"
		ts := httptest.NewServer(server.New(pool, cfg, logMailer{}, store, &server.Deps{
			Notify: notifications.LogSender{},
			Analytics: &analytics.Handler{
				Pool: pool, Tracker: tracker, MetricsKey: key,
			},
		}).Router())
		testEnv = &env{ts: ts, pool: pool, q: dbgen.New(pool), tracker: tracker, key: key}
	})
	if setupErr != nil {
		t.Skipf("analytics test setup skipped: %v", setupErr)
	}
	if testEnv == nil {
		t.Fatal("analytics test env nil")
	}
	return testEnv
}

func TestSanitizeProps(t *testing.T) {
	out := analytics.SanitizeProps(map[string]any{
		"capsule_id": "abc",
		"caption":    "secret",
		"is_own":     true,
		"nested":     map[string]any{"x": 1},
	})
	if _, ok := out["caption"]; ok {
		t.Fatal("caption must be stripped")
	}
	if out["capsule_id"] != "abc" || out["is_own"] != true {
		t.Fatalf("unexpected props: %#v", out)
	}
}

func signup(t *testing.T, e *env, email string) (userID uuid.UUID, token string) {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.q.CreateUser(context.Background(), dbgen.CreateUserParams{
		Email:        email,
		Username:     fmt.Sprintf("u%x", time.Now().UnixNano()%1e8),
		DisplayName:  "Tester",
		PasswordHash: hash,
		Timezone:     "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	userID = pg.UUIDValue(u.ID)
	body, _ := json.Marshal(map[string]string{
		"email": email, "password": "password123", "device_name": "test",
	})
	res, err := http.Post(e.ts.URL+"/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("login %d", res.StatusCode)
	}
	var payload struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	_ = json.NewDecoder(res.Body).Decode(&payload)
	if payload.Tokens.AccessToken == "" {
		t.Fatal("no access token")
	}
	return userID, payload.Tokens.AccessToken
}

func TestClientIngestAndSummary(t *testing.T) {
	e := getEnv(t)
	userID, token := signup(t, e, fmt.Sprintf("analytics-%d@example.com", time.Now().UnixNano()))

	unlock := time.Now().UTC().Add(48 * time.Hour)
	cap, err := e.q.CreateCapsule(context.Background(), dbgen.CreateCapsuleParams{
		CreatorID: pg.UUID(userID),
		Name:      "Trip",
		Type:      "solo",
		State:     "active",
		UnlockAt:  pg.Time(unlock),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.AddCapsuleMember(context.Background(), dbgen.AddCapsuleMemberParams{
		CapsuleID: cap.ID, UserID: pg.UUID(userID), Role: "admin",
		InviteStatus: "accepted", AcceptedAt: pg.Time(time.Now().UTC()),
	}); err != nil {
		t.Fatal(err)
	}

	capID := pg.UUIDValue(cap.ID).String()
	payload, _ := json.Marshal(map[string]any{
		"events": []map[string]any{
			{
				"name": "capsule_opened",
				"properties": map[string]any{
					"capsule_id":    capID,
					"capsule_state": "active",
					"caption":       "should-strip",
				},
			},
			{
				"name": "memory_viewed",
				"properties": map[string]any{
					"capsule_id": capID,
					"is_own":     false,
				},
			},
			{"name": "not_a_real_event", "properties": map[string]any{}},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/v1/analytics/events", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("ingest status %d", res.StatusCode)
	}
	var accepted struct {
		Accepted int `json:"accepted"`
	}
	_ = json.NewDecoder(res.Body).Decode(&accepted)
	if accepted.Accepted != 2 {
		t.Fatalf("accepted=%d want 2", accepted.Accepted)
	}

	e.tracker.FlushForTest()

	var n int
	err = e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM analytics_events WHERE user_id = $1 AND event_name = 'capsule_opened'`,
		pg.UUID(userID),
	).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatal("expected capsule_opened row")
	}

	var props []byte
	err = e.pool.QueryRow(context.Background(),
		`SELECT properties FROM analytics_events WHERE user_id = $1 AND event_name = 'capsule_opened' ORDER BY created_at DESC LIMIT 1`,
		pg.UUID(userID),
	).Scan(&props)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(props, &m)
	if _, ok := m["caption"]; ok {
		t.Fatalf("caption leaked: %s", props)
	}

	req2, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/v1/metrics/summary?days=90", nil)
	req2.Header.Set("X-Metrics-Key", e.key)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Fatalf("summary status %d", res2.StatusCode)
	}
	var summary analytics.Summary
	if err := json.NewDecoder(res2.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Metrics) != 15 {
		t.Fatalf("want 15 metrics, got %d", len(summary.Metrics))
	}
	ids := map[string]bool{}
	for _, met := range summary.Metrics {
		ids[met.ID] = true
	}
	for _, want := range []string{
		"signup_to_first_capsule_7d",
		"invite_conversion",
		"activated_capsule_rate",
		"disintegration_rate",
		"discovery_index",
		"repeat_capsule_rate_90d",
	} {
		if !ids[want] {
			t.Fatalf("missing metric %s", want)
		}
	}

	req3, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/v1/metrics/summary", nil)
	req3.Header.Set("X-Metrics-Key", "wrong")
	res3, _ := http.DefaultClient.Do(req3)
	defer res3.Body.Close()
	if res3.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", res3.StatusCode)
	}

	analytics.TrackUser(userID, analytics.EventCapsuleCreated, map[string]any{
		"capsule_id":   capID,
		"capsule_type": "solo",
	})
	e.tracker.FlushForTest()
}

func TestTrackDoesNotBlock(t *testing.T) {
	e := getEnv(t)
	start := time.Now()
	for i := 0; i < 100; i++ {
		analytics.Track(nil, analytics.EventAppOpened, nil)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("Track blocked too long: %s", time.Since(start))
	}
	e.tracker.FlushForTest()
}
