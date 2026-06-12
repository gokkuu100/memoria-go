package friends_test

// Integration tests for Phase B3 (friends & connections). Real Postgres only;
// skip if unreachable. Uses dedicated memoria_friends_test database.

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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_friends_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_friends_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_friends_test?sslmode=disable"
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
	AccessToken string `json:"access_token"`
}

type signupResp struct {
	User   map[string]any `json:"user"`
	Tokens tokensResp     `json:"tokens"`
}

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

func TestMutualFriendFlowViaInvite(t *testing.T) {
	e := getEnv(t)
	userA := signUp(t, e, uniq("alice")+"@example.com", uniq("alice_"))
	userB := signUp(t, e, uniq("bob")+"@example.com", uniq("bob_"))

	var invite struct {
		Token string `json:"token"`
	}
	if status := doJSON(t, "POST", e.ts.URL+"/v1/me/invite-link", userA.Tokens.AccessToken, nil, &invite); status != http.StatusOK || invite.Token == "" {
		t.Fatalf("invite link: status %d token %q", status, invite.Token)
	}

	var profile map[string]any
	if status := doJSON(t, "GET", e.ts.URL+"/v1/users/by-invite/"+invite.Token, userB.Tokens.AccessToken, nil, &profile); status != http.StatusOK {
		t.Fatalf("by-invite: status %d", status)
	}
	if profile["id"] != userA.User["id"] {
		t.Fatalf("by-invite id = %v, want %v", profile["id"], userA.User["id"])
	}

	targetID := userA.User["id"].(string)
	requesterID := userB.User["id"].(string)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil); status != http.StatusCreated {
		t.Fatalf("send request: status %d", status)
	}

	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests/"+requesterID+"/accept", userA.Tokens.AccessToken, nil, nil); status != http.StatusOK {
		t.Fatalf("accept request: status %d", status)
	}

	var friendsA struct {
		Friends []map[string]any `json:"friends"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/friends", userA.Tokens.AccessToken, nil, &friendsA); status != http.StatusOK {
		t.Fatalf("list friends A: status %d", status)
	}
	if len(friendsA.Friends) != 1 || friendsA.Friends[0]["id"] != userB.User["id"] {
		t.Fatalf("A friends = %+v, want B", friendsA.Friends)
	}

	var friendsB struct {
		Friends []map[string]any `json:"friends"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/friends", userB.Tokens.AccessToken, nil, &friendsB); status != http.StatusOK {
		t.Fatalf("list friends B: status %d", status)
	}
	if len(friendsB.Friends) != 1 || friendsB.Friends[0]["id"] != userA.User["id"] {
		t.Fatalf("B friends = %+v, want A", friendsB.Friends)
	}
}

func TestBlockSilence(t *testing.T) {
	e := getEnv(t)
	userA := signUp(t, e, uniq("carol")+"@example.com", uniq("carol_"))
	userB := signUp(t, e, uniq("dave")+"@example.com", uniq("dave_"))

	targetID := userA.User["id"].(string)
	requesterID := userB.User["id"].(string)
	usernameA := userA.User["username"].(string)

	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil)
	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests/"+requesterID+"/accept", userA.Tokens.AccessToken, nil, nil)

	if status := doJSON(t, "POST", e.ts.URL+"/v1/blocks/"+requesterID, userA.Tokens.AccessToken, nil, nil); status != http.StatusNoContent {
		t.Fatalf("block: status %d, want 204", status)
	}

	if status := doJSON(t, "GET", e.ts.URL+"/v1/users/"+usernameA, userB.Tokens.AccessToken, nil, nil); status != http.StatusNotFound {
		t.Fatalf("blocked profile view: status %d, want 404", status)
	}

	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil); status != http.StatusNotFound {
		t.Fatalf("blocked friend request: status %d, want 404", status)
	}
}

func TestFriendSearchAmongConnections(t *testing.T) {
	e := getEnv(t)
	userA := signUp(t, e, uniq("eve")+"@example.com", uniq("eve_"))
	userB := signUp(t, e, uniq("frank")+"@example.com", uniq("frank_"))

	targetID := userA.User["id"].(string)
	requesterID := userB.User["id"].(string)
	usernameB := userB.User["username"].(string)

	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil)
	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests/"+requesterID+"/accept", userA.Tokens.AccessToken, nil, nil)

	partial := usernameB[:len(usernameB)-2]
	var result struct {
		Friends []map[string]any `json:"friends"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/friends?q="+partial, userA.Tokens.AccessToken, nil, &result); status != http.StatusOK {
		t.Fatalf("search friends: status %d", status)
	}
	if len(result.Friends) != 1 || result.Friends[0]["username"] != usernameB {
		t.Fatalf("search result = %+v, want %s", result.Friends, usernameB)
	}
}
