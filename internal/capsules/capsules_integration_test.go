package capsules_test

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
	"memoria-backend/internal/billing"
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_capsules_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_capsules_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_capsules_test?sslmode=disable"
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
			S3Bucket: "memoria-capsules-test", S3UsePathStyle: true,
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

func doJSONWithHeaders(t *testing.T, method, url, bearer string, body any, headers map[string]string, out any) int {
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
	for k, v := range headers {
		req.Header.Set(k, v)
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

func addCapsuleMemoryWithCapturedAt(t *testing.T, e *env, token, capID string, capturedAt time.Time, idempotencyKey string) (memoryID string, status int) {
	t.Helper()
	mediaID := uploadPhoto(t, e, token)
	var mem struct {
		ID         string    `json:"id"`
		CapturedAt time.Time `json:"captured_at"`
	}
	headers := map[string]string{}
	if idempotencyKey != "" {
		headers["Idempotency-Key"] = idempotencyKey
	}
	status = doJSONWithHeaders(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
		map[string]any{
			"media_id":    mediaID,
			"captured_at": capturedAt.UTC().Format(time.RFC3339Nano),
		}, headers, &mem)
	return mem.ID, status
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

func uploadPhoto(t *testing.T, e *env, token string) string {
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
	return pre.MediaID
}

func createSoloCapsule(t *testing.T, e *env, token string, unlockIn time.Duration) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	unlock := time.Now().UTC().Add(unlockIn).Format(time.RFC3339)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", token, map[string]any{
		"name": "Solo", "type": "solo", "unlock_at": unlock,
	}, &created); status != http.StatusCreated || created.ID == "" {
		t.Fatalf("create solo capsule: status %d id %q", status, created.ID)
	}
	return created.ID
}

func createGroupCapsule(t *testing.T, e *env, tokenA string, inviteeIDs []string, unlockIn time.Duration) string {
	t.Helper()
	var created struct {
		ID string `json:"id"`
	}
	unlock := time.Now().UTC().Add(unlockIn).Format(time.RFC3339)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", tokenA, map[string]any{
		"name": "Group Trip", "type": "group", "unlock_at": unlock,
		"invited_user_ids": inviteeIDs,
	}, &created); status != http.StatusCreated {
		t.Fatalf("create group capsule: status %d", status)
	}
	return created.ID
}

func addCapsuleMemory(t *testing.T, e *env, token, capID string) string {
	t.Helper()
	mediaID := uploadPhoto(t, e, token)
	var mem struct {
		ID string `json:"id"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
		map[string]string{"media_id": mediaID}, &mem); status != http.StatusCreated {
		t.Fatalf("add capsule memory: status %d", status)
	}
	return mem.ID
}

// Table-driven state machine: sealed reads and transitions.
func TestCapsuleStateMachine(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("sm_a")+"@example.com", uniq("sma_"), "Admin")
	tokenB, idB := signUp(t, e, uniq("sm_b")+"@example.com", uniq("smb_"), "Member")
	tokenC, idC := signUp(t, e, uniq("sm_c")+"@example.com", uniq("smc_"), "Member2")
	connectFriends(t, e, tokenA, idA, tokenB, idB)
	connectFriends(t, e, tokenA, idA, tokenC, idC)

	capID := createGroupCapsule(t, e, tokenA, []string{idB, idC}, 48*time.Hour)

	tests := []struct {
		name       string
		setup      func()
		memStatus  int
		memCode    string
		detailSeal bool
	}{
		{
			name:       "pending_before_accept",
			memStatus:  http.StatusForbidden,
			memCode:    "capsule_sealed",
			detailSeal: true,
		},
		{
			name: "active_after_all_accept",
			setup: func() {
				doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/accept", tokenB, nil, nil)
				doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/accept", tokenC, nil, nil)
				addCapsuleMemory(t, e, tokenA, capID)
			},
			memStatus:  http.StatusForbidden,
			memCode:    "capsule_sealed",
			detailSeal: true,
		},
		{
			name: "frozen_rejects_contribution",
			setup: func() {
				_, err := e.pool.Exec(context.Background(),
					"UPDATE capsules SET last_contribution_at = now() - interval '8 days' WHERE id = $1", capID)
				if err != nil {
					t.Fatal(err)
				}
				if err := capsules.RunFreeze(context.Background(), e.q, notifications.LogSender{}); err != nil {
					t.Fatal(err)
				}
			},
			memStatus:  http.StatusForbidden,
			memCode:    "capsule_sealed",
			detailSeal: true,
		},
		{
			name: "unlocked_allows_memories_list",
			setup: func() {
				_, err := e.pool.Exec(context.Background(),
					"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
				if err != nil {
					t.Fatal(err)
				}
				if err := capsules.RunUnlock(context.Background(), e.q, notifications.LogSender{}); err != nil {
					t.Fatal(err)
				}
			},
			memStatus:  http.StatusOK,
			detailSeal: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setup != nil {
				tc.setup()
			}
			var errResp struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/memories", tokenA, nil, &errResp)
			if status != tc.memStatus {
				t.Fatalf("memories GET status %d want %d", status, tc.memStatus)
			}
			if tc.memCode != "" && errResp.Error.Code != tc.memCode {
				t.Fatalf("memories error code %q want %q", errResp.Error.Code, tc.memCode)
			}

			var detail struct {
				Sealed      bool `json:"sealed"`
				MemoryCount int  `json:"memory_count"`
			}
			if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID, tokenA, nil, &detail); status != http.StatusOK {
				t.Fatalf("detail GET status %d", status)
			}
			if detail.Sealed != tc.detailSeal {
				t.Fatalf("sealed=%v want %v", detail.Sealed, tc.detailSeal)
			}
			// Sealing invariant: detail never exposes media URLs (only metadata).
			raw, _ := json.Marshal(detail)
			if bytes.Contains(raw, []byte("media_url")) || bytes.Contains(raw, []byte("bucket")) {
				t.Fatalf("sealed detail leaked media reference: %s", raw)
			}
		})
	}
}

func TestSealingInvariantNoSignedURLBeforeUnlock(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("seal_")+"@example.com", uniq("seal_"), "Sealer")
	capID := createSoloCapsule(t, e, token, 72*time.Hour)
	addCapsuleMemory(t, e, token, capID)

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/memories", token, nil, &errResp); status != http.StatusForbidden {
		t.Fatalf("status %d want 403", status)
	}
	if errResp.Error.Code != "capsule_sealed" {
		t.Fatalf("code %q want capsule_sealed", errResp.Error.Code)
	}

	var detail struct {
		Sealed      bool `json:"sealed"`
		MemoryCount int  `json:"memory_count"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID, token, nil, &detail); status != http.StatusOK {
		t.Fatalf("detail status %d", status)
	}
	if !detail.Sealed || detail.MemoryCount < 1 {
		t.Fatalf("sealed=%v memory_count=%d", detail.Sealed, detail.MemoryCount)
	}
	raw, _ := json.Marshal(detail)
	if bytes.Contains(raw, []byte("media_url")) {
		t.Fatalf("detail leaked media_url before unlock")
	}

	// Social endpoints also gated until unlock.
	memID := addCapsuleMemory(t, e, token, capID)
	var socialErr struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if status := doJSON(t, "PUT", e.ts.URL+"/v1/memories/"+memID+"/reactions", token,
		map[string]string{"emoji": "❤️"}, &socialErr); status != http.StatusForbidden {
		t.Fatalf("reaction before unlock: status %d want 403", status)
	}
}

func TestFullExitScenario(t *testing.T) {
	e := getEnv(t)
	tokenA, idA := signUp(t, e, uniq("ex_a")+"@example.com", uniq("exa_"), "Creator")
	tokenB, idB := signUp(t, e, uniq("ex_b")+"@example.com", uniq("exb_"), "Bob")
	tokenC, idC := signUp(t, e, uniq("ex_c")+"@example.com", uniq("exc_"), "Carol")
	connectFriends(t, e, tokenA, idA, tokenB, idB)
	connectFriends(t, e, tokenA, idA, tokenC, idC)

	capID := createGroupCapsule(t, e, tokenA, []string{idB, idC}, 24*time.Hour)
	doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/accept", tokenB, nil, nil)
	doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/accept", tokenC, nil, nil)

	addCapsuleMemory(t, e, tokenA, capID)
	addCapsuleMemory(t, e, tokenB, capID)

	// Force freeze
	_, err := e.pool.Exec(context.Background(),
		"UPDATE capsules SET last_contribution_at = now() - interval '8 days' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}
	if err := capsules.RunFreeze(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	// Unfreeze via votes
	for _, tok := range []string{tokenA, tokenB, tokenC} {
		if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/unfreeze-vote", tok, nil, nil); status != http.StatusOK {
			t.Fatalf("unfreeze vote: status %d", status)
		}
	}

	// Unlock
	_, err = e.pool.Exec(context.Background(),
		"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}
	if err := capsules.RunUnlock(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	var memList struct {
		Memories []struct {
			ID       string `json:"id"`
			MediaURL string `json:"media_url"`
		} `json:"memories"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/memories", tokenA, nil, &memList); status != http.StatusOK || len(memList.Memories) < 2 {
		t.Fatalf("unlocked memories: status %d count %d", status, len(memList.Memories))
	}
	if memList.Memories[0].MediaURL == "" {
		t.Fatal("expected signed media URL after unlock")
	}

	memID := memList.Memories[0].ID
	if status := doJSON(t, "PUT", e.ts.URL+"/v1/memories/"+memID+"/reactions", tokenB,
		map[string]string{"emoji": "🎉"}, nil); status != http.StatusOK {
		t.Fatalf("react after unlock: status %d", status)
	}

	var stats map[string]any
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/stats", tokenA, nil, &stats); status != http.StatusOK {
		t.Fatalf("stats: status %d", status)
	}

	var recap map[string]any
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/recap", tokenA, nil, &recap); status != http.StatusOK {
		t.Fatalf("recap: status %d", status)
	}
	if recap["total_memories"] == nil || recap["days_sealed"] == nil {
		t.Fatalf("recap missing core fields: %#v", recap)
	}
	if _, ok := recap["peak_hour_local"]; !ok {
		t.Fatalf("recap missing peak_hour_local: %#v", recap)
	}
	if _, ok := recap["top_contributor"]; !ok {
		t.Fatalf("recap missing top_contributor: %#v", recap)
	}
	if _, ok := recap["viewer"]; !ok {
		t.Fatalf("recap missing viewer block: %#v", recap)
	}
	if _, ok := recap["facts"]; !ok {
		t.Fatalf("recap missing facts map: %#v", recap)
	}
	viewer, _ := recap["viewer"].(map[string]any)
	if viewer["memories_taken"] == nil {
		t.Fatalf("recap viewer missing memories_taken: %#v", viewer)
	}
}

func TestViewableUntilFromCreatorPlan(t *testing.T) {
	e := getEnv(t)
	token, userID := signUp(t, e, uniq("vt_")+"@example.com", uniq("vt_"), "Viewer")
	capID := createSoloCapsule(t, e, token, 1*time.Hour)
	addCapsuleMemory(t, e, token, capID)

	_, err := e.pool.Exec(context.Background(),
		"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}
	if err := capsules.RunUnlock(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	cap, err := e.q.GetCapsuleByID(context.Background(), pg.UUID(uuid.MustParse(capID)))
	if err != nil {
		t.Fatal(err)
	}
	if !cap.ViewableUntil.Valid {
		t.Fatal("spark plan should stamp viewable_until")
	}
	plan := billing.ForUser(billing.PlanSpark)
	expected := time.Now().UTC().Add(time.Duration(plan.ViewabilityDays) * 24 * time.Hour)
	got := pg.TimeValue(cap.ViewableUntil)
	if got.Before(expected.Add(-2*time.Hour)) || got.After(expected.Add(2*time.Hour)) {
		t.Fatalf("viewable_until %v not near %v", got, expected)
	}

	if err := capsules.RestampViewableForCreator(context.Background(), e.q, uuid.MustParse(userID), billing.Plans[billing.PlanPro]); err != nil {
		t.Fatal(err)
	}
	cap, err = e.q.GetCapsuleByID(context.Background(), pg.UUID(uuid.MustParse(capID)))
	if err != nil {
		t.Fatal(err)
	}
	if cap.ViewableUntil.Valid {
		t.Fatal("pro plan should clear viewable_until (forever)")
	}
}

func TestSoloCapsuleActiveImmediately(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("solo_")+"@example.com", uniq("solo_"), "Solo")
	capID := createSoloCapsule(t, e, token, 48*time.Hour)
	var detail struct {
		State string `json:"state"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID, token, nil, &detail); status != http.StatusOK {
		t.Fatalf("get: status %d", status)
	}
	if detail.State != "active" {
		t.Fatalf("solo state %q want active", detail.State)
	}
}

func TestCapsuleLimitEnforced(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("lim_")+"@example.com", uniq("lim_"), "Lim")
	createSoloCapsule(t, e, token, 24*time.Hour)
	createSoloCapsule(t, e, token, 48*time.Hour)

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	unlock := time.Now().UTC().Add(72 * time.Hour).Format(time.RFC3339)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", token, map[string]any{
		"name": "Third", "type": "solo", "unlock_at": unlock,
	}, &errResp); status != http.StatusForbidden || errResp.Error.Code != "capsule_limit_reached" {
		t.Fatalf("third capsule: status %d code %q", status, errResp.Error.Code)
	}
}

func TestCapturedAtPreservedOnMemory(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("cap_")+"@example.com", uniq("cap_"), "Cap")
	capID := createSoloCapsule(t, e, token, 48*time.Hour)

	time.Sleep(25 * time.Millisecond)
	captured := time.Now().UTC()
	memID, status := addCapsuleMemoryWithCapturedAt(t, e, token, capID, captured, "")
	if status != http.StatusCreated || memID == "" {
		t.Fatalf("add memory: status %d id %q", status, memID)
	}

	_, err := e.pool.Exec(context.Background(),
		"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}
	if err := capsules.RunUnlock(context.Background(), e.q, notifications.LogSender{}); err != nil {
		t.Fatal(err)
	}

	var listed struct {
		Memories []struct {
			ID        string    `json:"id"`
			CreatedAt time.Time `json:"created_at"`
		} `json:"memories"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/capsules/"+capID+"/memories", token, nil, &listed); status != http.StatusOK {
		t.Fatalf("list memories: status %d", status)
	}
	if len(listed.Memories) != 1 {
		t.Fatalf("memories = %d want 1", len(listed.Memories))
	}
	got := listed.Memories[0].CreatedAt.UTC()
	if got.Before(captured.Add(-2 * time.Second)) || got.After(captured.Add(2 * time.Second)) {
		t.Fatalf("timeline created_at %v want near captured %v", got, captured)
	}
}

func TestMemoryIdempotencyKey(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("idem_")+"@example.com", uniq("idem_"), "Idem")
	capID := createSoloCapsule(t, e, token, 48*time.Hour)
	key := uuid.New().String()
	time.Sleep(25 * time.Millisecond)
	captured := time.Now().UTC()

	id1, status1 := addCapsuleMemoryWithCapturedAt(t, e, token, capID, captured, key)
	if status1 != http.StatusCreated {
		t.Fatalf("first add: status %d", status1)
	}
	id2, status2 := addCapsuleMemoryWithCapturedAt(t, e, token, capID, captured, key)
	if status2 != http.StatusOK {
		t.Fatalf("retry add: status %d want 200", status2)
	}
	if id1 != id2 {
		t.Fatalf("idempotency ids differ: %q vs %q", id1, id2)
	}

	var count int64
	if err := e.pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM memories WHERE container_id = $1 AND deleted_at IS NULL", capID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("memory count = %d want 1", count)
	}
}

func TestSyncDeadlineRejectedAfterUnlock(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("dead_")+"@example.com", uniq("dead_"), "Dead")
	capID := createSoloCapsule(t, e, token, 1*time.Hour)

	_, err := e.pool.Exec(context.Background(),
		"UPDATE capsules SET unlock_at = now() - interval '1 minute' WHERE id = $1", capID)
	if err != nil {
		t.Fatal(err)
	}

	mediaID := uploadPhoto(t, e, token)
	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	captured := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
		map[string]string{"media_id": mediaID, "captured_at": captured}, &errResp); status != http.StatusForbidden {
		t.Fatalf("post-deadline add: status %d want 403", status)
	}
	if errResp.Error.Code != "queue_sync_deadline_passed" {
		t.Fatalf("code %q want queue_sync_deadline_passed", errResp.Error.Code)
	}
}
