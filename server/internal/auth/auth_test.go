package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hackathon2026/server/internal/store"
)

type fakeStore struct {
	mu       sync.Mutex
	users    map[string]store.User
	sessions map[string]string // token hash -> user id
}

func newFake() *fakeStore {
	return &fakeStore{users: map[string]store.User{}, sessions: map[string]string{}}
}

func (f *fakeStore) CreateUser(_ context.Context, username, hash string, _ time.Time) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[username]; ok {
		return store.User{}, store.ErrUsernameTaken
	}
	u := store.User{ID: "id-" + username, Username: username, PasswordHash: hash}
	f.users[username] = u
	return u, nil
}

func (f *fakeStore) UserByUsername(_ context.Context, username string) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[username]; ok {
		return u, nil
	}
	return store.User{}, store.ErrNotFound
}

func (f *fakeStore) CreateSession(_ context.Context, tokenHash, userID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[tokenHash] = userID
	return nil
}

func (f *fakeStore) UserBySession(_ context.Context, tokenHash string, _ time.Time) (store.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.sessions[tokenHash]
	if !ok {
		return store.User{}, store.ErrNotFound
	}
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return store.User{}, store.ErrNotFound
}

func (f *fakeStore) DeleteSession(_ context.Context, tokenHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, tokenHash)
	return nil
}

func serve(s *Service) http.Handler {
	mux := http.NewServeMux()
	s.Routes(mux)
	return mux
}

func post(h http.Handler, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sid(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

func TestRegisterLoginMeLogout(t *testing.T) {
	h := serve(New(newFake()))

	rec := post(h, "/api/register", `{"username":"Blue_Fox","password":"hunter2hunter2","consent":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body)
	}
	c := sid(rec)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie missing or not httpOnly/Lax: %+v", c)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(c)
	me := httptest.NewRecorder()
	h.ServeHTTP(me, req)
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"blue_fox"`) {
		t.Fatalf("me: %d %s", me.Code, me.Body)
	}

	if rec := post(h, "/api/logout", `{}`, c); rec.Code != http.StatusOK {
		t.Fatalf("logout: %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(c)
	me = httptest.NewRecorder()
	h.ServeHTTP(me, req)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", me.Code)
	}

	if rec := post(h, "/api/login", `{"username":"blue_fox","password":"hunter2hunter2"}`); rec.Code != http.StatusOK || sid(rec) == nil {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
}

func TestRegisterValidation(t *testing.T) {
	h := serve(New(newFake()))
	cases := map[string]string{
		"short username": `{"username":"ab","password":"hunter2hunter2","consent":true}`,
		"bad chars":      `{"username":"blue fox","password":"hunter2hunter2","consent":true}`,
		"short password": `{"username":"blue_fox","password":"short","consent":true}`,
		"long password":  `{"username":"blue_fox","password":"` + strings.Repeat("a", 73) + `","consent":true}`,
		"no consent":     `{"username":"blue_fox","password":"hunter2hunter2","consent":false}`,
		"bad json":       `{`,
	}
	for name, body := range cases {
		if rec := post(h, "/api/register", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, rec.Code)
		}
	}
}

func TestDuplicateUsernameAndWrongPassword(t *testing.T) {
	h := serve(New(newFake()))
	body := `{"username":"amber_owl","password":"hunter2hunter2","consent":true}`
	if rec := post(h, "/api/register", body); rec.Code != http.StatusCreated {
		t.Fatalf("first register: %d", rec.Code)
	}
	if rec := post(h, "/api/register", body); rec.Code != http.StatusConflict {
		t.Errorf("duplicate: want 409, got %d", rec.Code)
	}
	if rec := post(h, "/api/login", `{"username":"amber_owl","password":"wrong-password"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: want 401, got %d", rec.Code)
	}
	if rec := post(h, "/api/login", `{"username":"nobody","password":"whatever123"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown user: want 401, got %d", rec.Code)
	}
}

func TestRequiresJSONContentType(t *testing.T) {
	h := serve(New(newFake()))
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("username=a&password=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("want 415, got %d", rec.Code)
	}
}

func TestNoDatabase(t *testing.T) {
	h := serve(New(nil))
	if rec := post(h, "/api/login", `{"username":"a","password":"b"}`); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503, got %d", rec.Code)
	}
}

func TestSecureCookieBehindProxy(t *testing.T) {
	h := serve(New(newFake()))
	req := httptest.NewRequest(http.MethodPost, "/api/register",
		strings.NewReader(`{"username":"red_cat","password":"hunter2hunter2","consent":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if c := sid(rec); c == nil || !c.Secure {
		t.Fatalf("cookie should be Secure behind https proxy: %+v", c)
	}
}

func TestRateLimit(t *testing.T) {
	h := serve(New(newFake()))
	var last int
	for i := 0; i < 12; i++ {
		last = post(h, "/api/login", `{"username":"nobody","password":"whatever123"}`).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("want 429 after 10 attempts, got %d", last)
	}
}
