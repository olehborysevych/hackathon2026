// Package auth implements register/login/logout/me (SPEC §7): bcrypt passwords and an opaque
// session token in an httpOnly cookie. Only a SHA-256 of the token is stored.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"hackathon2026/server/internal/store"
)

const (
	CookieName  = "sid"
	SessionTTL  = 7 * 24 * time.Hour
	maxBodySize = 4 << 10
)

var usernameRE = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

// Store is the persistence the handlers need. *store.Store implements it.
type Store interface {
	CreateUser(ctx context.Context, username, passwordHash string, consentAt time.Time) (store.User, error)
	UserByUsername(ctx context.Context, username string) (store.User, error)
	CreateSession(ctx context.Context, tokenHash, userID string, expires time.Time) error
	UserBySession(ctx context.Context, tokenHash string, now time.Time) (store.User, error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

type Service struct {
	store   Store // nil when DATABASE_URL is not configured: auth endpoints answer 503
	now     func() time.Time
	limiter *limiter
	// dummyHash is compared against when a username does not exist, so login timing does not
	// reveal which usernames are registered.
	dummyHash []byte
}

func New(s Store) *Service {
	dummy, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	return &Service{store: s, now: time.Now, limiter: newLimiter(10, time.Minute), dummyHash: dummy}
}

func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/register", s.wrap(s.register))
	mux.HandleFunc("POST /api/login", s.wrap(s.login))
	mux.HandleFunc("POST /api/logout", s.wrap(s.logout))
	mux.HandleFunc("GET /api/me", s.wrap(s.me))
}

// wrap adds the checks shared by all auth endpoints.
func (s *Service) wrap(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.store == nil {
			writeErr(w, http.StatusServiceUnavailable, "Accounts are not available yet (no database configured)")
			return
		}
		// Requiring JSON blocks cross-site form posts (a browser cannot send this type without a
		// CORS preflight), which together with SameSite=Lax covers CSRF for these endpoints.
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
			return
		}
		h(w, r)
	}
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Consent  bool   `json:"consent"`
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r), s.now()) {
		writeErr(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	c.Username = strings.ToLower(strings.TrimSpace(c.Username))
	switch {
	case !usernameRE.MatchString(c.Username):
		writeErr(w, http.StatusBadRequest, "Username must be 3–20 characters: a–z, 0–9, underscore.")
		return
	case len(c.Password) < 8:
		writeErr(w, http.StatusBadRequest, "Password must be at least 8 characters.")
		return
	case len(c.Password) > 72: // bcrypt ignores everything past 72 bytes
		writeErr(w, http.StatusBadRequest, "Password must be at most 72 bytes.")
		return
	case !c.Consent:
		writeErr(w, http.StatusBadRequest, "You need to agree to the research consent to create an account.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		serverErr(w, "hash password", err)
		return
	}
	user, err := s.store.CreateUser(r.Context(), c.Username, string(hash), s.now())
	if errors.Is(err, store.ErrUsernameTaken) {
		writeErr(w, http.StatusConflict, "That username is taken.")
		return
	}
	if err != nil {
		serverErr(w, "create user", err)
		return
	}
	if !s.startSession(w, r, user) {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"username": user.Username})
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r), s.now()) {
		writeErr(w, http.StatusTooManyRequests, "Too many attempts. Try again in a minute.")
		return
	}
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	c.Username = strings.ToLower(strings.TrimSpace(c.Username))

	user, err := s.store.UserByUsername(r.Context(), c.Username)
	switch {
	case errors.Is(err, store.ErrNotFound):
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(c.Password))
		writeErr(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	case err != nil:
		serverErr(w, "load user", err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(c.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "Wrong username or password.")
		return
	}
	if !s.startSession(w, r, user) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			slog.Error("delete session", "err", err)
		}
	}
	http.SetCookie(w, s.cookie(r, "", -1))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Not signed in.")
		return
	}
	user, err := s.store.UserBySession(r.Context(), hashToken(c.Value), s.now())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusUnauthorized, "Not signed in.")
		return
	}
	if err != nil {
		serverErr(w, "load session", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": user.Username})
}

func (s *Service) startSession(w http.ResponseWriter, r *http.Request, user store.User) bool {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		serverErr(w, "random token", err)
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.store.CreateSession(r.Context(), hashToken(token), user.ID, s.now().Add(SessionTTL)); err != nil {
		serverErr(w, "create session", err)
		return false
	}
	http.SetCookie(w, s.cookie(r, token, int(SessionTTL.Seconds())))
	return true
}

func (s *Service) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	}
}

// isHTTPS is true behind a TLS-terminating proxy such as Railway's edge, or on direct TLS.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request body.")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeErr(w, http.StatusInternalServerError, "Something went wrong on our side.")
}

// clientIP uses the last X-Forwarded-For entry (the one the nearest proxy appended, which a
// client cannot forge) and falls back to the socket address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter is a small fixed-window counter per key. Single process by design (CLAUDE.md), so
// in-memory is enough.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newLimiter(max int, w time.Duration) *limiter {
	return &limiter{max: max, window: w, hits: map[string]*window{}}
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10000 { // bound memory under a flood of distinct keys
		for k, w := range l.hits {
			if now.Sub(w.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w, ok := l.hits[key]
	if !ok || now.Sub(w.start) > l.window {
		l.hits[key] = &window{start: now, n: 1}
		return true
	}
	w.n++
	return w.n <= l.max
}
