package albums_test

// Integration tests for Phase B5 (albums full lifecycle). Real Postgres + MinIO;
// skip if unreachable. Uses dedicated memoria_albums_test database.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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
	"memoria-backend/internal/albums"
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_albums_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_albums_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_albums_test?sslmode=disable"
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
			S3Bucket: "memoria-albums-test", S3UsePathStyle: true,
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
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadPhoto(t *testing.T, e *env, token string) (mediaID, bucketKey string) {
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
	row, err := e.q.GetMediaByID(context.Background(), pg.UUID(uuid.MustParse(pre.MediaID)))
	if err != nil {
		t.Fatal(err)
	}
	return pre.MediaID, row.BucketKey
}

func createAcceptAlbum(t *testing.T, e *env, tokenA, idB, tokenB string) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums", tokenA, map[string]any{
		"name": "Test Album", "cover_style": "espresso", "invited_user_id": idB,
	}, &created); status != http.StatusCreated || created.ID == "" {
		t.Fatalf("create album: status %d id %q", status, created.ID)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+created.ID+"/accept", tokenB, nil, nil); status != http.StatusOK {
		t.Fatalf("accept album: status %d", status)
	}
	return created.ID
}

func TestPendingAlbumInActiveList(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("plist_a")+"@example.com", uniq("plista_"), "PlA")
	tokenB, idB := signUp(t, e, uniq("plist_b")+"@example.com", uniq("plistb_"), "PlB")
	connectFriends(t, e, tokenA, idA, tokenB, idB)

	var created struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums", tokenA, map[string]any{
		"name": "Pending List", "cover_style": "gold", "invited_user_id": idB,
	}, &created); status != http.StatusCreated || created.State != "pending" {
		t.Fatalf("create: status %d state %q", status, created.State)
	}

	var activeList struct {
		Albums []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"albums"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums?state=active", tokenA, nil, &activeList); status != http.StatusOK {
		t.Fatalf("creator active list: status %d", status)
	}
	if len(activeList.Albums) != 1 || activeList.Albums[0].ID != created.ID || activeList.Albums[0].State != "pending" {
		t.Fatalf("creator active list: %+v want pending album %q", activeList.Albums, created.ID)
	}

	var inviteList struct {
		Albums []struct {
			ID string `json:"id"`
		} `json:"albums"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums?state=invites", tokenB, nil, &inviteList); status != http.StatusOK || len(inviteList.Albums) != 1 {
		t.Fatalf("invitee invites list: status %d count %d", status, len(inviteList.Albums))
	}

	var bActiveList struct {
		Albums []struct {
			ID string `json:"id"`
		} `json:"albums"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums?state=active", tokenB, nil, &bActiveList); status != http.StatusOK || len(bActiveList.Albums) != 0 {
		t.Fatalf("invitee active list before accept: count %d want 0", len(bActiveList.Albums))
	}

	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+created.ID+"/decline", tokenB, nil, nil); status != http.StatusOK {
		t.Fatalf("decline: status %d", status)
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums?state=invites", tokenB, nil, &inviteList); status != http.StatusOK || len(inviteList.Albums) != 0 {
		t.Fatalf("invitee invites after decline: count %d want 0", len(inviteList.Albums))
	}
}

func TestFullAlbumLifecycle(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("alb_a")+"@example.com", uniq("alice_"), "Alice")
	tokenB, idB := signUp(t, e, uniq("alb_b")+"@example.com", uniq("bob_"), "Bob")
	connectFriends(t, e, tokenA, idA, tokenB, idB)

	albumID := createAcceptAlbum(t, e, tokenA, idB, tokenB)

	mediaID, bucketKey := uploadPhoto(t, e, tokenA)
	var mem struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+albumID+"/memories", tokenA,
		map[string]string{"media_id": mediaID, "caption": "hello"}, &mem); status != http.StatusCreated {
		t.Fatalf("add memory: status %d", status)
	}

	var list struct {
		Memories []struct {
			ID            string `json:"id"`
			ReactionCount int64  `json:"reaction_count"`
			CommentCount  int64  `json:"comment_count"`
		} `json:"memories"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums/"+albumID+"/memories", tokenB, nil, &list); status != http.StatusOK || len(list.Memories) != 1 {
		t.Fatalf("list memories: status %d count %d", status, len(list.Memories))
	}

	if status := doJSON(t, "PUT", e.ts.URL+"/v1/memories/"+mem.ID+"/reactions", tokenB,
		map[string]string{"emoji": "❤️"}, nil); status != http.StatusOK {
		t.Fatalf("react: status %d", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/memories/"+mem.ID+"/comments", tokenB,
		map[string]string{"body": "nice shot"}, nil); status != http.StatusCreated {
		t.Fatalf("comment: status %d", status)
	}

	// Leaver (Alice) deletes her photos from R2.
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+albumID+"/leave", tokenA, nil, nil); status != http.StatusOK {
		t.Fatalf("leave: status %d", status)
	}
	info, err := e.store.Head(context.Background(), bucketKey)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("leaver media still in R2 after leave")
	}
	_ = idA
}

func TestPhotoCapRejected(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("cap_a")+"@example.com", uniq("capa_"), "CapA")
	tokenB, idB := signUp(t, e, uniq("cap_b")+"@example.com", uniq("capb_"), "CapB")
	connectFriends(t, e, tokenA, idA, tokenB, idB)
	albumID := createAcceptAlbum(t, e, tokenA, idB, tokenB)

	for i := 0; i < 30; i++ {
		mediaID, _ := uploadPhoto(t, e, tokenA)
		if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+albumID+"/memories", tokenA,
			map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
			t.Fatalf("photo %d: status %d", i+1, status)
		}
	}
	mediaID, _ := uploadPhoto(t, e, tokenA)
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+albumID+"/memories", tokenA,
		map[string]string{"media_id": mediaID}, &errResp); status != http.StatusForbidden || errResp.Error.Code != "album_photo_limit_reached" {
		t.Fatalf("31st photo: status %d code %q", status, errResp.Error.Code)
	}
}

func TestExpireInvitesDisintegrates(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("exp_a")+"@example.com", uniq("expa_"), "ExpA")
	tokenB, idB := signUp(t, e, uniq("exp_b")+"@example.com", uniq("expb_"), "ExpB")
	connectFriends(t, e, tokenA, idA, tokenB, idB)

	var created struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums", tokenA, map[string]any{
		"name": "Pending", "cover_style": "gold", "invited_user_id": idB,
	}, &created); status != http.StatusCreated {
		t.Fatalf("create: status %d", status)
	}

	_, err := e.pool.Exec(context.Background(),
		"UPDATE albums SET invite_expires_at = now() - interval '1 hour' WHERE id = $1", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := albums.ExpireInvites(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	if status := doJSON(t, "GET", e.ts.URL+"/v1/albums/"+created.ID, tokenA, nil, nil); status != http.StatusNotFound {
		t.Fatalf("expired album GET: status %d, want 404", status)
	}
	_ = tokenB
}

func TestTimelineDayBuckets(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("tl_a")+"@example.com", uniq("tla_"), "TlA")
	tokenB, idB := signUp(t, e, uniq("tl_b")+"@example.com", uniq("tlb_"), "TlB")
	connectFriends(t, e, tokenA, idA, tokenB, idB)
	albumID := createAcceptAlbum(t, e, tokenA, idB, tokenB)

	mediaID, _ := uploadPhoto(t, e, tokenA)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/albums/"+albumID+"/memories", tokenA,
		map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
		t.Fatalf("add memory: status %d", status)
	}

	month := time.Now().UTC().Format("2006-01")
	var tl struct {
		Month string `json:"month"`
		Days  []struct {
			Date  string `json:"date"`
			Items []struct {
				ThumbURL string `json:"thumb_url"`
			} `json:"items"`
		} `json:"days"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/timeline?month="+month, tokenA, nil, &tl); status != http.StatusOK {
		t.Fatalf("timeline: status %d", status)
	}
	if tl.Month != month {
		t.Fatalf("month = %q want %q", tl.Month, month)
	}
	if len(tl.Days) == 0 || len(tl.Days[0].Items) == 0 || tl.Days[0].Items[0].ThumbURL == "" {
		t.Fatalf("expected day bucket with thumb URL: %+v", tl.Days)
	}
}
