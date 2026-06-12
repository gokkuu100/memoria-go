package notifications_test

// Integration tests for Phase B4 (push notification core). Real Postgres only;
// Expo Push API is mocked via httptest. Uses memoria_notifications_test DB.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
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

type expoMock struct {
	mu            sync.Mutex
	pushCalls     int
	lastMessages  []map[string]any
	receiptErrors map[string]string // ticket id -> error code
}

func (m *expoMock) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/push/send":
			var msgs []map[string]any
			_ = json.Unmarshal(body, &msgs)
			m.mu.Lock()
			m.pushCalls++
			m.lastMessages = msgs
			m.mu.Unlock()
			tickets := make([]map[string]string, len(msgs))
			for i := range msgs {
				tickets[i] = map[string]string{"status": "ok", "id": fmt.Sprintf("ticket%d", i+1)}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": tickets})
		case r.Method == http.MethodPost && r.URL.Path == "/push/getReceipts":
			var req struct {
				IDs []string `json:"ids"`
			}
			_ = json.Unmarshal(body, &req)
			data := make(map[string]any)
			m.mu.Lock()
			for _, id := range req.IDs {
				if errCode, ok := m.receiptErrors[id]; ok {
					data[id] = map[string]any{
						"status":  "error",
						"message": errCode,
						"details": map[string]string{"error": errCode},
					}
				} else {
					data[id] = map[string]string{"status": "ok"}
				}
			}
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		default:
			http.NotFound(w, r)
		}
	})
}

func (m *expoMock) pushCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pushCalls
}

func (m *expoMock) lastTo() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.lastMessages) == 0 {
		return ""
	}
	to, _ := m.lastMessages[0]["to"].(string)
	return to
}

type env struct {
	ts     *httptest.Server
	expo   *httptest.Server
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	outbox *notifications.OutboxService
	mailer *capturingMailer
	mock   *expoMock
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
		if _, err := admin.Exec("DROP DATABASE IF EXISTS memoria_notifications_test WITH (FORCE)"); err != nil {
			setupErr = err
			return
		}
		if _, err := admin.Exec("CREATE DATABASE memoria_notifications_test"); err != nil {
			setupErr = err
			return
		}

		testURL := "postgres://memoria:memoria@localhost:5432/memoria_notifications_test?sslmode=disable"
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
		q := dbgen.New(pool)
		mock := &expoMock{}
		expoSrv := httptest.NewServer(mock.handler())
		expoClient := notifications.NewExpoClient(expoSrv.URL+"/push/send", "")
		outbox := notifications.NewOutboxService(pool, q, expoClient)

		m := &capturingMailer{}
		cfg := &config.Config{
			Env: config.EnvDevelopment, JWTSecret: "test-secret",
			S3Endpoint: "http://localhost:9000", S3PublicEndpoint: "http://localhost:9000",
			S3AccessKey: "memoria", S3SecretKey: "memoria-secret",
			S3Bucket: "memoria-media", S3UsePathStyle: true,
			ExpoPushURL: expoSrv.URL + "/push/send",
		}
		store, err := media.NewStore(context.Background(), cfg)
		if err != nil {
			setupErr = err
			return
		}
		testEnv = &env{
			ts: httptest.NewServer(server.New(pool, cfg, m, store, &server.Deps{
				Notify: outbox,
				Notif:  &notifications.Handler{Q: q, Flusher: outbox},
			}).Router()),
			expo:   expoSrv,
			pool:   pool,
			q:      q,
			outbox: outbox,
			mailer: m,
			mock:   mock,
		}
	})
	if setupErr != nil {
		t.Skipf("integration environment unavailable: %v", setupErr)
	}
	return testEnv
}

func resetMock(t *testing.T, e *env) *expoMock {
	t.Helper()
	mock := &expoMock{}
	e.expo.Config.Handler = mock.handler()
	e.mock = mock
	return mock
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

func registerPushToken(t *testing.T, e *env, token, expoToken string) {
	t.Helper()
	status := doJSON(t, "PUT", e.ts.URL+"/v1/me/push-token", token, map[string]string{
		"token": expoToken, "platform": "ios",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("push token: status %d", status)
	}
}

func seedTestUser(t *testing.T, e *env, username string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	email := username + "@example.com"
	_, err := e.pool.Exec(context.Background(),
		`INSERT INTO users (id, email, username, display_name, password_hash) VALUES ($1, $2, $3, $4, $5)`,
		id, email, username, "Test User", "noop")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func registerPushTokenDirect(t *testing.T, e *env, userID uuid.UUID, expoToken string) {
	t.Helper()
	if err := e.q.UpsertPushToken(context.Background(), dbgen.UpsertPushTokenParams{
		UserID:    pg.UUID(userID),
		ExpoToken: expoToken,
		Platform:  "ios",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFriendRequestEnqueuesAndSendsPush(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)

	userA := signUp(t, e, uniq("alice")+"@example.com", uniq("alice_"))
	userB := signUp(t, e, uniq("bob")+"@example.com", uniq("bob_"))
	expoToken := "ExponentPushToken[test-friend-req]"
	registerPushToken(t, e, userA.Tokens.AccessToken, expoToken)

	targetID := userA.User["id"].(string)
	if status := doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil); status != http.StatusCreated {
		t.Fatalf("send request: status %d", status)
	}

	e.outbox.FlushInstant(context.Background())

	if mock.pushCount() != 1 {
		t.Fatalf("expo push calls = %d, want 1", mock.pushCount())
	}
	if got := mock.lastTo(); got != expoToken {
		t.Fatalf("push to = %q, want %q", got, expoToken)
	}

	var list struct {
		Notifications []map[string]any `json:"notifications"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/notifications", userA.Tokens.AccessToken, nil, &list); status != http.StatusOK {
		t.Fatalf("list notifications: status %d", status)
	}
	if len(list.Notifications) != 1 {
		t.Fatalf("notifications = %d, want 1", len(list.Notifications))
	}
	if list.Notifications[0]["category"] != notifications.CategoryFriendRequests {
		t.Fatalf("category = %v", list.Notifications[0]["category"])
	}
}

func TestPrefDisabledSkipsExpo(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)

	userA := signUp(t, e, uniq("carol")+"@example.com", uniq("carol_"))
	userB := signUp(t, e, uniq("dave")+"@example.com", uniq("dave_"))
	registerPushToken(t, e, userA.Tokens.AccessToken, "ExponentPushToken[disabled-pref]")

	status := doJSON(t, "PUT", e.ts.URL+"/v1/me/notification-prefs", userA.Tokens.AccessToken, map[string]any{
		"prefs": []map[string]any{{"category": notifications.CategoryFriendRequests, "enabled": false}},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("put prefs: status %d", status)
	}

	targetID := userA.User["id"].(string)
	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil)
	e.outbox.FlushInstant(context.Background())

	if mock.pushCount() != 0 {
		t.Fatalf("expo push calls = %d, want 0 when pref disabled", mock.pushCount())
	}
}

func TestDeadTokenPruned(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)
	mock.receiptErrors = map[string]string{"ticket1": "DeviceNotRegistered"}

	userA := signUp(t, e, uniq("eve")+"@example.com", uniq("eve_"))
	userB := signUp(t, e, uniq("frank")+"@example.com", uniq("frank_"))
	deadToken := "ExponentPushToken[dead-device]"
	registerPushToken(t, e, userA.Tokens.AccessToken, deadToken)

	targetID := userA.User["id"].(string)
	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil)
	e.outbox.FlushInstant(context.Background())

	userID, _ := uuid.Parse(userA.User["id"].(string))
	tokens, err := e.q.ListPushTokensForUser(context.Background(), pg.UUID(userID))
	if err != nil {
		t.Fatal(err)
	}
	if len(tokens) != 0 {
		t.Fatalf("push tokens after prune = %d, want 0", len(tokens))
	}
}

func TestNotificationPrefsDefaultsAndUpdate(t *testing.T) {
	e := getEnv(t)
	user := signUp(t, e, uniq("gina")+"@example.com", uniq("gina_"))

	var prefs struct {
		Prefs []struct {
			Category string `json:"category"`
			Enabled  bool   `json:"enabled"`
		} `json:"prefs"`
	}
	if status := doJSON(t, "GET", e.ts.URL+"/v1/me/notification-prefs", user.Tokens.AccessToken, nil, &prefs); status != http.StatusOK {
		t.Fatalf("get prefs: status %d", status)
	}
	if len(prefs.Prefs) != len(notifications.AllCategories) {
		t.Fatalf("prefs count = %d, want %d", len(prefs.Prefs), len(notifications.AllCategories))
	}
	for _, p := range prefs.Prefs {
		if !p.Enabled {
			t.Fatalf("default pref %s should be enabled", p.Category)
		}
	}

	doJSON(t, "PUT", e.ts.URL+"/v1/me/notification-prefs", user.Tokens.AccessToken, map[string]any{
		"prefs": []map[string]any{{"category": notifications.CategoryAlbumMemory, "enabled": false}},
	}, &prefs)
	for _, p := range prefs.Prefs {
		if p.Category == notifications.CategoryAlbumMemory && p.Enabled {
			t.Fatal("album_memory should be disabled after PUT")
		}
	}
}

func (m *expoMock) lastBody() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.lastMessages) == 0 {
		return ""
	}
	body, _ := m.lastMessages[0]["body"].(string)
	return body
}

func TestCapsuleMemoryBatchCollapse(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)

	recipientID := seedTestUser(t, e, uniq("batch_rcpt_"))
	expoToken := "ExponentPushToken[capsule-batch]"
	registerPushTokenDirect(t, e, recipientID, expoToken)

	capsuleID := uuid.New()
	past := time.Now().UTC().Add(-time.Minute)
	for i := 0; i < 3; i++ {
		batchKey := fmt.Sprintf("capsule:%s:%s", capsuleID, recipientID)
		data := map[string]any{
			"type":         "capsule_memory_added",
			"capsule_id":   capsuleID.String(),
			"from_user_id": uuid.New().String(),
			"capsule_name": "Wales Trip",
		}
		if err := e.outbox.EnqueueAt(context.Background(), recipientID, notifications.CategoryCapsuleMemory,
			"New memory", "added a memory", data, &batchKey, past); err != nil {
			t.Fatal(err)
		}
	}

	e.outbox.FlushBatches(context.Background())

	if mock.pushCount() != 1 {
		t.Fatalf("expo push calls = %d, want 1", mock.pushCount())
	}
	body := mock.lastBody()
	if !strings.Contains(body, "3 new memories") || !strings.Contains(body, "Wales Trip") {
		t.Fatalf("push body = %q, want collapsed batch message", body)
	}
}

func TestCapsuleMemoryPrefDisabledSkipsBatch(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)

	userID := seedTestUser(t, e, uniq("cap_pref_"))
	registerPushTokenDirect(t, e, userID, "ExponentPushToken[capsule-pref-off]")

	if err := e.q.UpsertNotificationPref(context.Background(), dbgen.UpsertNotificationPrefParams{
		UserID:   pg.UUID(userID),
		Category: notifications.CategoryCapsuleMemory,
		Enabled:  false,
	}); err != nil {
		t.Fatal(err)
	}

	capsuleID := uuid.New()
	batchKey := fmt.Sprintf("capsule:%s:%s", capsuleID, userID)
	past := time.Now().UTC().Add(-time.Minute)
	data := map[string]any{"type": "capsule_memory_added", "capsule_name": "Test Cap"}
	if err := e.outbox.EnqueueAt(context.Background(), userID, notifications.CategoryCapsuleMemory,
		"New memory", "added", data, &batchKey, past); err != nil {
		t.Fatal(err)
	}

	e.outbox.FlushBatches(context.Background())

	if mock.pushCount() != 0 {
		t.Fatalf("expo push calls = %d, want 0 when capsule_memory pref disabled", mock.pushCount())
	}
}

func TestReactionBatchCollapse(t *testing.T) {
	e := getEnv(t)
	mock := resetMock(t, e)

	authorID := seedTestUser(t, e, uniq("react_auth_"))
	expoToken := "ExponentPushToken[reaction-batch]"
	registerPushTokenDirect(t, e, authorID, expoToken)

	memoryID := uuid.New()
	past := time.Now().UTC().Add(-time.Minute)

	for i := 0; i < 2; i++ {
		batchKey := fmt.Sprintf("reaction:%s:%s", memoryID, authorID)
		data := map[string]any{
			"type":         "reaction_on_memory",
			"memory_id":    memoryID.String(),
			"from_user_id": uuid.New().String(),
		}
		if err := e.outbox.EnqueueAt(context.Background(), authorID, notifications.CategoryReactionsComments,
			"New reaction", "reacted to your photo", data, &batchKey, past); err != nil {
			t.Fatal(err)
		}
	}

	e.outbox.FlushBatches(context.Background())

	if mock.pushCount() != 1 {
		t.Fatalf("expo push calls = %d, want 1", mock.pushCount())
	}
	body := mock.lastBody()
	if !strings.Contains(body, "2 new reactions") {
		t.Fatalf("push body = %q, want collapsed reaction message", body)
	}
}

func TestMarkReadReducesUnreadCount(t *testing.T) {
	e := getEnv(t)
	resetMock(t, e)

	userA := signUp(t, e, uniq("helen")+"@example.com", uniq("helen_"))
	userB := signUp(t, e, uniq("ivan")+"@example.com", uniq("ivan_"))
	registerPushToken(t, e, userA.Tokens.AccessToken, "ExponentPushToken[read-test]")

	targetID := userA.User["id"].(string)
	doJSON(t, "POST", e.ts.URL+"/v1/friends/requests", userB.Tokens.AccessToken,
		map[string]string{"user_id": targetID}, nil)
	e.outbox.FlushInstant(context.Background())

	var unread struct {
		Count int64 `json:"count"`
	}
	doJSON(t, "GET", e.ts.URL+"/v1/notifications/unread-count", userA.Tokens.AccessToken, nil, &unread)
	if unread.Count != 1 {
		t.Fatalf("unread = %d, want 1", unread.Count)
	}

	var list struct {
		Notifications []map[string]any `json:"notifications"`
	}
	doJSON(t, "GET", e.ts.URL+"/v1/notifications", userA.Tokens.AccessToken, nil, &list)
	notifID := list.Notifications[0]["id"].(string)

	doJSON(t, "POST", e.ts.URL+"/v1/notifications/"+notifID+"/read", userA.Tokens.AccessToken, nil, nil)
	doJSON(t, "GET", e.ts.URL+"/v1/notifications/unread-count", userA.Tokens.AccessToken, nil, &unread)
	if unread.Count != 0 {
		t.Fatalf("unread after mark read = %d, want 0", unread.Count)
	}
}
