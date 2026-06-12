package billing_test

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

const (
	testWebhookSecret = "test-revenuecat-secret"
	testProductPlus   = "memoria_plus"
	testProductPro    = "memoria_pro"
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_billing_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_billing_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_billing_test?sslmode=disable"
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
			Env:                     config.EnvDevelopment,
			JWTSecret:               "test-secret",
			S3Endpoint:              "http://localhost:9000",
			S3PublicEndpoint:        "http://localhost:9000",
			S3AccessKey:             "memoria",
			S3SecretKey:             "memoria-secret",
			S3Bucket:                "memoria-billing-test",
			S3UsePathStyle:          true,
			RevenueCatWebhookSecret: testWebhookSecret,
			RevenueCatProductPlus:   testProductPlus,
			RevenueCatProductPro:    testProductPro,
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

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec
}

func sendWebhook(t *testing.T, e *env, eventID, eventType, userID, productID string, expMs int64) int {
	t.Helper()
	body := map[string]any{
		"api_version": "1.0",
		"event": map[string]any{
			"id":               eventID,
			"type":             eventType,
			"app_user_id":      userID,
			"product_id":       productID,
			"expiration_at_ms": expMs,
		},
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.ts.URL+"/v1/webhooks/revenuecat", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testWebhookSecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
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

func addCapsuleMemory(t *testing.T, e *env, token, capID string) {
	t.Helper()
	mediaID := uploadPhoto(t, e, token)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules/"+capID+"/memories", token,
		map[string]string{"media_id": mediaID}, nil); status != http.StatusCreated {
		t.Fatalf("add capsule memory: status %d", status)
	}
}

func TestWebhookIdempotency(t *testing.T) {
	e := getEnv(t)
	_, userID := signUp(t, e, uniq("wh_")+"@example.com", uniq("wh_"), "Webhook")
	eventID := "evt_" + uuid.NewString()
	exp := time.Now().Add(30 * 24 * time.Hour).UnixMilli()

	if status := sendWebhook(t, e, eventID, "INITIAL_PURCHASE", userID, testProductPlus, exp); status != http.StatusOK {
		t.Fatalf("first webhook: status %d", status)
	}
	user, err := e.q.GetUserByID(context.Background(), pg.UUID(uuid.MustParse(userID)))
	if err != nil {
		t.Fatal(err)
	}
	if user.Plan != billing.PlanPlus {
		t.Fatalf("plan %q want plus", user.Plan)
	}

	if status := sendWebhook(t, e, eventID, "INITIAL_PURCHASE", userID, testProductPlus, exp); status != http.StatusOK {
		t.Fatalf("duplicate webhook: status %d", status)
	}
	count, err := e.q.GetSubscriptionEventByRCID(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	if count.RevenuecatEventID != eventID {
		t.Fatal("expected subscription event row")
	}
}

func TestUpgradeRestampsViewableUntil(t *testing.T) {
	e := getEnv(t)
	token, userID := signUp(t, e, uniq("up_")+"@example.com", uniq("up_"), "Upgrade")
	capID := createSoloCapsule(t, e, token, time.Hour)
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

	exp := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if status := sendWebhook(t, e, "evt_"+uuid.NewString(), "INITIAL_PURCHASE", userID, testProductPro, exp); status != http.StatusOK {
		t.Fatalf("upgrade webhook: status %d", status)
	}

	cap, err = e.q.GetCapsuleByID(context.Background(), pg.UUID(uuid.MustParse(capID)))
	if err != nil {
		t.Fatal(err)
	}
	if cap.ViewableUntil.Valid {
		t.Fatal("pro upgrade should clear viewable_until (forever)")
	}
}

func TestDowngradeBlocksNewCapsuleWhenOverLimit(t *testing.T) {
	e := getEnv(t)
	token, userID := signUp(t, e, uniq("dn_")+"@example.com", uniq("dn_"), "Downgrade")

	exp := time.Now().Add(30 * 24 * time.Hour).UnixMilli()
	if status := sendWebhook(t, e, "evt_"+uuid.NewString(), "INITIAL_PURCHASE", userID, testProductPro, exp); status != http.StatusOK {
		t.Fatalf("pro upgrade: status %d", status)
	}

	createSoloCapsule(t, e, token, 24*time.Hour)
	createSoloCapsule(t, e, token, 48*time.Hour)
	createSoloCapsule(t, e, token, 72*time.Hour)

	if status := sendWebhook(t, e, "evt_"+uuid.NewString(), "EXPIRATION", userID, "", 0); status != http.StatusOK {
		t.Fatalf("expiration webhook: status %d", status)
	}

	user, err := e.q.GetUserByID(context.Background(), pg.UUID(uuid.MustParse(userID)))
	if err != nil {
		t.Fatal(err)
	}
	if user.Plan != billing.PlanSpark {
		t.Fatalf("plan %q want spark after expiration", user.Plan)
	}

	var errResp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	unlock := time.Now().UTC().Add(96 * time.Hour).Format(time.RFC3339)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/capsules", token, map[string]any{
		"name": "Fourth", "type": "solo", "unlock_at": unlock,
	}, &errResp); status != http.StatusForbidden || errResp.Error.Code != "capsule_limit_reached" {
		t.Fatalf("new capsule after downgrade: status %d code %q", status, errResp.Error.Code)
	}
}

func TestGetSubscription(t *testing.T) {
	e := getEnv(t)
	token, _ := signUp(t, e, uniq("sub_")+"@example.com", uniq("sub_"), "Sub")
	var sub struct {
		Plan   string `json:"plan"`
		Limits struct {
			MaxActiveCapsules *int `json:"max_active_capsules"`
		} `json:"limits"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/me/subscription", token, nil, &sub); status != http.StatusOK {
		t.Fatalf("get subscription: status %d", status)
	}
	if sub.Plan != billing.PlanSpark {
		t.Fatalf("plan %q want spark", sub.Plan)
	}
	if sub.Limits.MaxActiveCapsules == nil || *sub.Limits.MaxActiveCapsules != 2 {
		t.Fatalf("max_active_capsules want 2, got %v", sub.Limits.MaxActiveCapsules)
	}
}
