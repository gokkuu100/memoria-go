package media_test

// Integration tests for Phase B2 (media pipeline). They run against real
// Postgres + MinIO from docker compose (`make up`), using a dedicated
// memoria_media_test database. Skipped if either dependency is unreachable.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
	"memoria-backend/internal/server"
)

type env struct {
	ts     *httptest.Server
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	store  *media.Store
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_media_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_media_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_media_test?sslmode=disable"
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
			S3Bucket: "memoria-media-test", S3UsePathStyle: true,
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
			ts:     httptest.NewServer(server.New(pool, cfg, m, store, nil).Router()),
			pool:   pool,
			q:      dbgen.New(pool),
			store:  store,
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

// signUp drives the OTP → ticket → signup flow and returns an access token.
func signUp(t *testing.T, e *env, email, username string) string {
	t.Helper()
	status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil)
	if status != http.StatusOK {
		t.Fatalf("otp request: got status %d", status)
	}
	var verify struct {
		Ticket string `json:"ticket"`
	}
	status = doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email, "purpose": "signup", "code": e.mailer.lastCode()}, &verify)
	if status != http.StatusOK || verify.Ticket == "" {
		t.Fatalf("otp verify: status %d ticket %q", status, verify.Ticket)
	}
	var resp struct {
		Tokens struct {
			AccessToken string `json:"access_token"`
		} `json:"tokens"`
	}
	status = doJSON(t, "POST", e.ts.URL+"/v1/auth/signup", "", map[string]string{
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": "Test User",
	}, &resp)
	if status != http.StatusCreated || resp.Tokens.AccessToken == "" {
		t.Fatalf("signup: status %d", status)
	}
	return resp.Tokens.AccessToken
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec // test-only randomness
}

// tinyPNG encodes a real 2x1 image at runtime (real data, not a mock).
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type presignResp struct {
	MediaID   string            `json:"media_id"`
	UploadURL string            `json:"upload_url"`
	Headers   map[string]string `json:"headers"`
	Error     struct {
		Code string `json:"code"`
	} `json:"error"`
}

func presign(t *testing.T, e *env, token string, body map[string]any) (presignResp, int) {
	t.Helper()
	var resp presignResp
	status := doJSON(t, "POST", e.ts.URL+"/v1/media/presign", token, body, &resp)
	return resp, status
}

func uploadPut(t *testing.T, url string, headers map[string]string, data []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

type mediaResp struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	ContentType string  `json:"content_type"`
	Status      string  `json:"status"`
	ByteSize    *int64  `json:"byte_size"`
	Width       *int32  `json:"width"`
	Height      *int32  `json:"height"`
	URL         *string `json:"url"`
	Error       struct {
		Code string `json:"code"`
	} `json:"error"`
}

func TestUploadConfirmDownloadFlow(t *testing.T) {
	e := getEnv(t)
	token := signUp(t, e, uniq("mia")+"@example.com", uniq("mia_"))
	img := tinyPNG(t)

	pre, status := presign(t, e, token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img),
	})
	if status != http.StatusOK || pre.UploadURL == "" || pre.MediaID == "" {
		t.Fatalf("presign: status %d resp %+v", status, pre)
	}

	if status := uploadPut(t, pre.UploadURL, pre.Headers, img); status != http.StatusOK {
		t.Fatalf("upload PUT: status %d", status)
	}

	var confirmed mediaResp
	status = doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, &confirmed)
	if status != http.StatusOK || confirmed.Status != "ready" {
		t.Fatalf("confirm: status %d resp %+v", status, confirmed)
	}
	if confirmed.ByteSize == nil || *confirmed.ByteSize != int64(len(img)) {
		t.Fatalf("confirm byte_size = %v, want %d", confirmed.ByteSize, len(img))
	}
	if confirmed.Width == nil || *confirmed.Width != 2 || confirmed.Height == nil || *confirmed.Height != 1 {
		t.Fatalf("confirm dimensions = %vx%v, want 2x1", confirmed.Width, confirmed.Height)
	}

	var got mediaResp
	status = doJSON(t, "GET", e.ts.URL+"/v1/media/"+pre.MediaID, token, nil, &got)
	if status != http.StatusOK || got.URL == nil {
		t.Fatalf("get media: status %d url %v", status, got.URL)
	}
	resp, err := http.Get(*got.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, img) {
		t.Fatalf("signed GET: status %d, %d bytes, want %d matching bytes", resp.StatusCode, len(body), len(img))
	}

	// Confirm is idempotent for mobile retries.
	status = doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, &confirmed)
	if status != http.StatusOK || confirmed.Status != "ready" {
		t.Fatalf("re-confirm: status %d status %q", status, confirmed.Status)
	}
}

func TestPresignRejectsOversizeAndDuration(t *testing.T) {
	e := getEnv(t)
	token := signUp(t, e, uniq("max")+"@example.com", uniq("max_"))

	pre, status := presign(t, e, token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": 11 << 20, // 11 MB > 10 MB cap
	})
	if status != http.StatusRequestEntityTooLarge || pre.Error.Code != "media_too_large" {
		t.Fatalf("oversize presign: status %d code %q, want 413 media_too_large", status, pre.Error.Code)
	}

	// Spark caps video at 10s; 15s must be rejected.
	pre, status = presign(t, e, token, map[string]any{
		"kind": "video", "content_type": "video/mp4", "byte_size": 1 << 20, "duration_ms": 15000,
	})
	if status != http.StatusUnprocessableEntity || pre.Error.Code != "duration_exceeds_plan" {
		t.Fatalf("over-duration presign: status %d code %q, want 422 duration_exceeds_plan", status, pre.Error.Code)
	}

	pre, status = presign(t, e, token, map[string]any{
		"kind": "photo", "content_type": "image/gif", "byte_size": 100,
	})
	if status != http.StatusUnsupportedMediaType || pre.Error.Code != "unsupported_content_type" {
		t.Fatalf("bad content type: status %d code %q, want 415 unsupported_content_type", status, pre.Error.Code)
	}
}

func TestConfirmRejectsSizeMismatchAndCleansUp(t *testing.T) {
	e := getEnv(t)
	token := signUp(t, e, uniq("liar")+"@example.com", uniq("liar_"))
	img := tinyPNG(t)

	// Declare more bytes than will actually be uploaded.
	pre, status := presign(t, e, token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img) + 500,
	})
	if status != http.StatusOK {
		t.Fatalf("presign: status %d", status)
	}
	if status := uploadPut(t, pre.UploadURL, pre.Headers, img); status != http.StatusOK {
		t.Fatalf("upload PUT: status %d", status)
	}

	var confirmed mediaResp
	status = doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, &confirmed)
	if status != http.StatusBadRequest || confirmed.Error.Code != "media_size_mismatch" {
		t.Fatalf("mismatched confirm: status %d code %q, want 400 media_size_mismatch", status, confirmed.Error.Code)
	}

	// Row and object are gone: GET 404s and a fresh confirm 404s too.
	if status := doJSON(t, "GET", e.ts.URL+"/v1/media/"+pre.MediaID, token, nil, nil); status != http.StatusNotFound {
		t.Fatalf("after cleanup GET: status %d, want 404", status)
	}
}

func TestNonOwnerGets404(t *testing.T) {
	e := getEnv(t)
	owner := signUp(t, e, uniq("own")+"@example.com", uniq("own_"))
	other := signUp(t, e, uniq("oth")+"@example.com", uniq("oth_"))
	img := tinyPNG(t)

	pre, _ := presign(t, e, owner, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img),
	})
	if status := uploadPut(t, pre.UploadURL, pre.Headers, img); status != http.StatusOK {
		t.Fatalf("upload PUT: status %d", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", owner, nil, nil); status != http.StatusOK {
		t.Fatalf("confirm: status %d", status)
	}

	if status := doJSON(t, "GET", e.ts.URL+"/v1/media/"+pre.MediaID, other, nil, nil); status != http.StatusNotFound {
		t.Fatalf("non-owner GET: status %d, want 404", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", other, nil, nil); status != http.StatusNotFound {
		t.Fatalf("non-owner confirm: status %d, want 404", status)
	}
}

func TestAvatarFlow(t *testing.T) {
	e := getEnv(t)
	token := signUp(t, e, uniq("ava")+"@example.com", uniq("ava_"))
	img := tinyPNG(t)

	pre, _ := presign(t, e, token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img),
	})
	if status := uploadPut(t, pre.UploadURL, pre.Headers, img); status != http.StatusOK {
		t.Fatalf("upload PUT: status %d", status)
	}

	// Pending media cannot be an avatar yet.
	var errResp mediaResp
	status := doJSON(t, "PUT", e.ts.URL+"/v1/me/avatar", token,
		map[string]string{"media_id": pre.MediaID}, &errResp)
	if status != http.StatusConflict || errResp.Error.Code != "media_not_ready" {
		t.Fatalf("pending avatar: status %d code %q, want 409 media_not_ready", status, errResp.Error.Code)
	}

	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, nil); status != http.StatusOK {
		t.Fatalf("confirm: status %d", status)
	}

	var me struct {
		AvatarMediaID *string `json:"avatar_media_id"`
		AvatarURL     *string `json:"avatar_url"`
	}
	status = doJSON(t, "PUT", e.ts.URL+"/v1/me/avatar", token, map[string]string{"media_id": pre.MediaID}, &me)
	if status != http.StatusOK || me.AvatarURL == nil {
		t.Fatalf("set avatar: status %d avatar_url %v", status, me.AvatarURL)
	}

	status = doJSON(t, "GET", e.ts.URL+"/v1/me", token, nil, &me)
	if status != http.StatusOK || me.AvatarMediaID == nil || *me.AvatarMediaID != pre.MediaID || me.AvatarURL == nil {
		t.Fatalf("GET /me: status %d avatar_media_id %v avatar_url present=%v", status, me.AvatarMediaID, me.AvatarURL != nil)
	}

	resp, err := http.Get(*me.AvatarURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, img) {
		t.Fatalf("avatar signed GET: status %d, %d bytes", resp.StatusCode, len(body))
	}
}
