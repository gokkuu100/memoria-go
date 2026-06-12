package auth_test

// Integration tests for Phase B1 (auth & identity). They run against a real
// Postgres (docker compose: `make up`) and create a dedicated memoria_test
// database. If Postgres is unreachable the tests are skipped.

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

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"memoria-backend/db"
	"memoria-backend/internal/config"
	"memoria-backend/internal/dbgen"
	"memoria-backend/internal/media"
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_test?sslmode=disable"
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
		m := &capturingMailer{}
		cfg := &config.Config{
			Env: config.EnvDevelopment, JWTSecret: "test-secret",
			S3Endpoint: "http://localhost:9000", S3PublicEndpoint: "http://localhost:9000",
			S3AccessKey: "memoria", S3SecretKey: "memoria-secret",
			S3Bucket: "memoria-media", S3UsePathStyle: true,
		}
		store, err := media.NewStore(context.Background(), cfg)
		if err != nil {
			setupErr = err
			return
		}
		testEnv = &env{
			ts:     httptest.NewServer(server.New(pool, cfg, m, store, nil).Router()),
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

// doJSON sends a JSON request and decodes the JSON response into out (if non-nil).
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

type tokensResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type signupResp struct {
	User   map[string]any `json:"user"`
	Tokens tokensResp     `json:"tokens"`
}

// signUp drives the full OTP → ticket → signup flow and returns the response.
func signUp(t *testing.T, e *env, email, username string) signupResp {
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
	var resp signupResp
	status = doJSON(t, "POST", e.ts.URL+"/v1/auth/signup", "", map[string]string{
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": "Test User",
	}, &resp)
	if status != http.StatusCreated || resp.Tokens.AccessToken == "" {
		t.Fatalf("signup: status %d resp %+v", status, resp)
	}
	return resp
}

func uniq(prefix string) string {
	return fmt.Sprintf("%s%d", prefix, rand.Int63n(1_000_000_000)) //nolint:gosec // test-only randomness
}

func TestSignupLoginRefreshFlow(t *testing.T) {
	e := getEnv(t)
	email := uniq("alice") + "@example.com"
	username := uniq("alice_")

	resp := signUp(t, e, email, username)
	if resp.User["username"] != username {
		t.Fatalf("user.username = %v, want %s", resp.User["username"], username)
	}

	// /me with the access token.
	var me map[string]any
	if status := doJSON(t, "GET", e.ts.URL+"/v1/me", resp.Tokens.AccessToken, nil, &me); status != http.StatusOK {
		t.Fatalf("GET /me: status %d", status)
	}
	if me["email"] != email {
		t.Fatalf("me.email = %v, want %s", me["email"], email)
	}

	// Duplicate email: requesting a signup OTP again must 409.
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil); status != http.StatusConflict {
		t.Fatalf("duplicate email otp request: status %d, want 409", status)
	}

	// Login.
	var login signupResp
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/login", "",
		map[string]string{"email": email, "password": "supersecret1"}, &login); status != http.StatusOK {
		t.Fatalf("login: status %d", status)
	}

	// Refresh rotation: new pair issued, old refresh token becomes invalid.
	var rotated tokensResp
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/refresh", "",
		map[string]string{"refresh_token": login.Tokens.RefreshToken}, &rotated); status != http.StatusOK {
		t.Fatalf("refresh: status %d", status)
	}
	if rotated.RefreshToken == login.Tokens.RefreshToken {
		t.Fatal("refresh token was not rotated")
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/refresh", "",
		map[string]string{"refresh_token": login.Tokens.RefreshToken}, nil); status != http.StatusUnauthorized {
		t.Fatalf("old refresh token still valid: status %d, want 401", status)
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/refresh", "",
		map[string]string{"refresh_token": rotated.RefreshToken}, nil); status != http.StatusOK {
		t.Fatal("rotated refresh token should be valid")
	}

	// Wrong password is a generic 401.
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/login", "",
		map[string]string{"email": email, "password": "wrongpassword"}, nil); status != http.StatusUnauthorized {
		t.Fatalf("wrong password login: status %d, want 401", status)
	}
}

func TestUsernameRetiredForever(t *testing.T) {
	e := getEnv(t)
	email := uniq("bob") + "@example.com"
	username := uniq("bob_")

	resp := signUp(t, e, email, username)

	// Username availability flips to false while taken.
	var avail struct {
		Available bool `json:"available"`
	}
	doJSON(t, "GET", e.ts.URL+"/v1/auth/username-available?u="+username, "", nil, &avail)
	if avail.Available {
		t.Fatal("taken username reported available")
	}

	// Delete the account.
	if status := doJSON(t, "DELETE", e.ts.URL+"/v1/me", resp.Tokens.AccessToken, nil, nil); status != http.StatusOK {
		t.Fatalf("delete account: status %d", status)
	}

	// Username stays retired forever.
	doJSON(t, "GET", e.ts.URL+"/v1/auth/username-available?u="+username, "", nil, &avail)
	if avail.Available {
		t.Fatal("retired username reported available")
	}

	// Email is freed for a new account, but the retired username is rejected.
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil); status != http.StatusOK {
		t.Fatalf("freed email otp request: status %d, want 200", status)
	}
	var verify struct {
		Ticket string `json:"ticket"`
	}
	doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email, "purpose": "signup", "code": e.mailer.lastCode()}, &verify)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/signup", "", map[string]string{
		"ticket": verify.Ticket, "password": "supersecret1", "username": username, "display_name": "Bob Again",
	}, nil); status != http.StatusConflict {
		t.Fatalf("retired username signup: status %d, want 409", status)
	}
}

func TestOTPAttemptLimitAndExpiry(t *testing.T) {
	e := getEnv(t)
	email := uniq("carol") + "@example.com"

	if status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/request", "",
		map[string]string{"email": email, "purpose": "signup"}, nil); status != http.StatusOK {
		t.Fatalf("otp request: status %d", status)
	}

	// 5 wrong attempts allowed, the 6th is rejected outright.
	for i := 0; i < 5; i++ {
		status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
			map[string]string{"email": email, "purpose": "signup", "code": "000000"}, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("wrong code attempt %d: status %d, want 400", i+1, status)
		}
	}
	status := doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email, "purpose": "signup", "code": e.mailer.lastCode()}, nil)
	if status != http.StatusTooManyRequests {
		t.Fatalf("after max attempts: status %d, want 429", status)
	}

	// Expired codes are never accepted: insert one already expired.
	email2 := uniq("dave") + "@example.com"
	_, err := e.q.CreateOTP(context.Background(), dbgen.CreateOTPParams{
		Email:     email2,
		CodeHash:  "irrelevant",
		Purpose:   "signup",
		ExpiresAt: pg.Time(time.Now().Add(-time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	status = doJSON(t, "POST", e.ts.URL+"/v1/auth/otp/verify", "",
		map[string]string{"email": email2, "purpose": "signup", "code": "123456"}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("expired otp verify: status %d, want 400", status)
	}
}
