package widget_test

// Integration tests for Phase B10 (widget feed + thumbnails). Real Postgres +
// MinIO; dedicated memoria_widget_test database.

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/capsules"
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_widget_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_widget_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_widget_test?sslmode=disable"
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
			S3Bucket: "memoria-widget-test", S3UsePathStyle: true,
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

func signUp(t *testing.T, e *env, email, username, displayName string) (token, userID string) {
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
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": displayName,
	}, &resp); status != http.StatusCreated {
		t.Fatalf("signup: status %d", status)
	}
	return resp.Tokens.AccessToken, resp.User.ID
}

func connectFriends(t *testing.T, e *env, tokenA, idA, tokenB, idB string) {
	t.Helper()
	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", tokenB,
		map[string]string{"user_id": idA}, nil); status != http.StatusCreated {
		t.Fatalf("friend request: status %d", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests/"+idB+"/accept", tokenA, nil, nil); status != http.StatusOK {
		t.Fatalf("accept friend: status %d", status)
	}
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func confirmPhoto(t *testing.T, e *env, token string) (mediaID string, row dbgen.Medium) {
	t.Helper()
	img := tinyPNG(t)
	var pre struct {
		MediaID   string            `json:"media_id"`
		UploadURL string            `json:"upload_url"`
		Headers   map[string]string `json:"headers"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/presign", token, map[string]any{
		"kind": "photo", "content_type": "image/png", "byte_size": len(img),
	}, &pre); status != http.StatusOK {
		t.Fatalf("presign: status %d", status)
	}
	req, _ := http.NewRequest(http.MethodPut, pre.UploadURL, bytes.NewReader(img))
	for k, v := range pre.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if status := doJSON(t, "POST", e.ts.URL+"/v1/media/"+pre.MediaID+"/confirm", token, nil, nil); status != http.StatusOK {
		t.Fatalf("confirm: status %d", status)
	}
	row, err = e.q.GetMediaByID(context.Background(), pg.UUID(uuid.MustParse(pre.MediaID)))
	if err != nil {
		t.Fatal(err)
	}
	return pre.MediaID, row
}

type feedResp struct {
	Items []struct {
		Type        string  `json:"type"`
		AlbumID     string  `json:"album_id"`
		CapsuleID   string  `json:"capsule_id"`
		ThumbURL    string  `json:"thumb_url"`
		UnlockAt    *string `json:"unlock_at"`
	} `json:"items"`
}

func TestPhotoConfirmCreatesR2Thumbnail(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("wthumb")+"@example.com", uniq("wthumb_"), "Thumb User")
	mediaID, row := confirmPhoto(t, e, token)

	if row.ThumbBucketKey == nil || *row.ThumbBucketKey == "" {
		t.Fatal("expected thumb_bucket_key on media row")
	}
	ownerID := pg.UUIDValue(row.OwnerID)
	wantKey := media.ThumbKey(ownerID, uuid.MustParse(mediaID))
	if *row.ThumbBucketKey != wantKey {
		t.Fatalf("thumb key = %q, want %q", *row.ThumbBucketKey, wantKey)
	}

	info, err := e.store.Head(context.Background(), *row.ThumbBucketKey)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("thumb object missing in R2")
	}
	if info.ContentType != "image/jpeg" {
		t.Fatalf("thumb content type = %q, want image/jpeg", info.ContentType)
	}
}

func TestWidgetFeedIncludesAlbumThumb(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("wfa")+"@example.com", uniq("wfa_"), "Alice")
	tokenB, idB := signUp(t, e, uniq("wfb")+"@example.com", uniq("wfb_"), "Bob")
	connectFriends(t, e, tokenA, idA, tokenB, idB)

	var album struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums", tokenA, map[string]any{
		"name": "Widget Album", "cover_style": "espresso", "invited_user_id": idB,
	}, &album); status != http.StatusCreated {
		t.Fatalf("create album: status %d", status)
	}
	doJSON(t, "POST", e.ts.URL+"/v1/albums/"+album.ID+"/accept", tokenB, nil, nil)

	mediaID, _ := confirmPhoto(t, e, tokenA)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+album.ID+"/memories", tokenA,
		map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
		t.Fatalf("add memory: status %d", status)
	}

	var feed feedResp
	if status := doJSON(t, "GET", e.ts.URL+"/v1/widget/feed", tokenB, nil, &feed); status != http.StatusOK {
		t.Fatalf("widget feed: status %d", status)
	}
	found := false
	for _, item := range feed.Items {
		if item.Type == "album_photo" && item.AlbumID == album.ID && item.ThumbURL != "" {
			found = true
			resp, err := http.Get(item.ThumbURL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || len(body) == 0 {
				t.Fatalf("thumb GET: status %d len %d", resp.StatusCode, len(body))
			}
		}
	}
	if !found {
		t.Fatalf("expected album_photo with thumb_url in feed: %+v", feed.Items)
	}
}

func TestWidgetFeedSealedCapsuleCountdownOnly(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("wseal")+"@example.com", uniq("wseal_"), "Sealer")
	capID := func() string {
		var created struct {
			ID string `json:"id"`
		}
		unlock := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
		if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", token, map[string]any{
			"name": "Sealed Widget", "type": "solo", "unlock_at": unlock,
		}, &created); status != http.StatusCreated {
			t.Fatalf("create capsule: status %d", status)
		}
		return created.ID
	}()

	mediaID, _ := confirmPhoto(t, e, token)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
		map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
		t.Fatalf("add capsule memory: status %d", status)
	}

	var feed feedResp
	if status := doJSON(t, "GET", e.ts.URL+"/v1/widget/feed", token, nil, &feed); status != http.StatusOK {
		t.Fatalf("widget feed: status %d", status)
	}

	hasCountdown := false
	for _, item := range feed.Items {
		if item.CapsuleID != capID {
			continue
		}
		if item.Type == "capsule_unlocked" || (item.Type != "capsule_countdown" && item.ThumbURL != "") {
			t.Fatalf("sealed capsule must not expose media in feed: %+v", item)
		}
		if item.Type == "capsule_countdown" {
			if item.UnlockAt == nil || *item.UnlockAt == "" {
				t.Fatal("countdown missing unlock_at")
			}
			hasCountdown = true
		}
	}
	if !hasCountdown {
		t.Fatalf("expected capsule_countdown for sealed capsule %s: %+v", capID, feed.Items)
	}
}

func TestWidgetFeedUnlockedTodayCapsule(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("wunl")+"@example.com", uniq("wunl_"), "Unlocker")
	capID := func() string {
		var created struct {
			ID string `json:"id"`
		}
		unlock := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
		if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", token, map[string]any{
			"name": "Unlock Today", "type": "solo", "unlock_at": unlock,
		}, &created); status != http.StatusCreated {
			t.Fatalf("create capsule: status %d", status)
		}
		return created.ID
	}()
	addCapsuleMemory := func() {
		mediaID, _ := confirmPhoto(t, e, token)
		if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
			map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
			t.Fatalf("add memory: status %d", status)
		}
	}
	addCapsuleMemory()

	_, err := e.pool.Exec(context.Background(),
		"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}
	if err := capsules.RunUnlock(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	var feed feedResp
	if status := doJSON(t, "GET", e.ts.URL+"/v1/widget/feed", token, nil, &feed); status != http.StatusOK {
		t.Fatalf("widget feed: status %d", status)
	}
	found := false
	for _, item := range feed.Items {
		if item.CapsuleID == capID && item.Type == "capsule_unlocked" && item.ThumbURL != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected capsule_unlocked with thumb for %s: %+v", capID, feed.Items)
	}
}
