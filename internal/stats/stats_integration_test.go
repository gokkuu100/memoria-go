package stats_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/notifications"
	"memoria-backend/internal/pg"
	"memoria-backend/internal/server"
)

type env struct {
	ts     *httptest.Server
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	mailer *capturingMailer
}

var (
	setupOnce sync.Once
	testEnv   *env
	setupErr  error
)

type capturingMailer struct {
	mu   sync.Mutex
	last string
}

func (m *capturingMailer) Send(_ context.Context, _, _, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.last = body
	return nil
}

var codeRe = regexp.MustCompile(`\d{6}`)

func (m *capturingMailer) lastCode() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return codeRe.FindString(m.last)
}

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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_stats_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_stats_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_stats_test?sslmode=disable"
		mig, err := sql.Open("pgx", testURL)
		if err != nil {
			setupErr = err
			return
		}
		defer func() { _ = mig.Close() }()
		goose.SetBaseFS(db.Migrations)
		goose.SetLogger(goose.NopLogger())
		if err := goose.SetDialect("postgres"); err != nil {
			setupErr = err
			return
		}
		if err := goose.Up(mig, "migrations"); err != nil {
			setupErr = err
			return
		}

		pool, err := pgxpool.New(context.Background(), testURL)
		if err != nil {
			setupErr = err
			return
		}

		cfg := &config.Config{
			Env: config.EnvDevelopment, JWTSecret: "test-secret",
			S3Endpoint: "http://localhost:9000", S3PublicEndpoint: "http://localhost:9000",
			S3AccessKey: "memoria", S3SecretKey: "memoria-secret",
			S3Bucket: "memoria-stats-test", S3UsePathStyle: true,
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

		m := &capturingMailer{}
		testEnv = &env{
			ts: httptest.NewServer(server.New(pool, cfg, m, store, &server.Deps{
				Notify: notifications.LogSender{},
			}).Router()),
			pool:   pool,
			q:      dbgen.New(pool),
			mailer: m,
		}
	})
	if setupErr != nil {
		t.Skipf("integration environment unavailable: %v", setupErr)
	}
	return testEnv
}

func doJSON(t *testing.T, method, url, bearer string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
	}
	return resp.StatusCode
}

func signUp(t *testing.T, e *env, email, username string) (token, userID string) {
	t.Helper()
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil); status != http.StatusOK {
		t.Fatalf("otp request: status %d", status)
	}
	var verify struct {
		Ticket string `json:"ticket"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email, "purpose": "signup", "code": e.mailer.lastCode()}, &verify); status != http.StatusOK {
		t.Fatalf("otp verify: status %d", status)
	}
	var resp struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/signup", "", map[string]string{
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": "Stats User",
	}, &resp); status != http.StatusCreated {
		t.Fatalf("signup: status %d", status)
	}
	return resp.Tokens.AccessToken, resp.User.ID
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec
}

func TestHeatmapSparkThreeMonthWindow(t *testing.T) {
	e := getEnv(t)
	token, userID := signUp(t, e, uniq("stats")+"@example.com", uniq("stats_"))
	uid, err := uuid.Parse(userID)
	if err != nil {
		t.Fatal(err)
	}

	mediaRow, err := e.q.CreateMedia(context.Background(), dbgen.CreateMediaParams{
		OwnerID: pg.UUID(uid), Kind: "photo", BucketKey: "media/test/old.png",
		ContentType: "image/png", ByteSize: ptrInt64(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.MarkMediaReady(context.Background(), dbgen.MarkMediaReadyParams{
		ID: mediaRow.ID, ByteSize: ptrInt64(100), Width: ptrInt32(1), Height: ptrInt32(1),
	}); err != nil {
		t.Fatal(err)
	}

	oldDay := time.Now().UTC().AddDate(0, -4, 0)
	recentDay := time.Now().UTC().AddDate(0, 0, -2)

	album, err := e.q.CreateAlbum(context.Background(), dbgen.CreateAlbumParams{
		CreatorID: pg.UUID(uid), Name: "Stats Album", CoverStyle: "espresso",
		InviteExpiresAt: pg.Time(time.Now().Add(48 * time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.q.AddAlbumMember(context.Background(), dbgen.AddAlbumMemberParams{
		AlbumID: album.ID, UserID: pg.UUID(uid), InviteStatus: "accepted",
		AcceptedAt: pg.Time(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.q.ActivateAlbum(context.Background(), album.ID); err != nil {
		t.Fatal(err)
	}

	for _, day := range []time.Time{oldDay, recentDay} {
		if _, err := e.pool.Exec(context.Background(), `
			INSERT INTO memories (container_type, container_id, author_id, media_id, created_at, captured_at)
			VALUES ('album', $1, $2, $3, $4, $4)
		`, album.ID, uid, mediaRow.ID, day); err != nil {
			t.Fatal(err)
		}
	}

	var heatmap map[string]any
	status := doJSON(t, "GET", e.ts.URL+"/v1/me/stats/heatmap?from=2020-01-01", token, nil, &heatmap)
	if status != http.StatusOK {
		t.Fatalf("heatmap: status %d", status)
	}
	from, _ := heatmap["from"].(string)
	days, _ := heatmap["days"].([]any)
	if from == "2020-01-01" {
		t.Fatalf("spark plan should clamp from date, got %s", from)
	}
	if len(days) != 1 {
		t.Fatalf("expected 1 heatmap day within 3-month window, got %d (from=%s)", len(days), from)
	}
}

func TestLifetimeStats(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("life")+"@example.com", uniq("life_"))

	var stats map[string]any
	if status := doJSON(t, "GET", e.ts.URL+"/v1/me/stats/lifetime", token, nil, &stats); status != http.StatusOK {
		t.Fatalf("lifetime: status %d", status)
	}
	if stats["memories"].(float64) != 0 {
		t.Fatalf("expected 0 memories, got %v", stats["memories"])
	}
}

func ptrInt64(v int64) *int64 { return &v }
func ptrInt32(v int32) *int32 { return &v }
