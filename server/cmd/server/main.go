// Command server is the single deployable: HTTP API, /ws game hub and the embedded static web UI
// (SPEC §5). One process, one instance: game state lives in memory, Postgres is the record.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hackathon2026/server/internal/agent"
	"hackathon2026/server/internal/auth"
	"hackathon2026/server/internal/game"
	"hackathon2026/server/internal/hub"
	"hackathon2026/server/internal/store"
)

// web is the Next.js static export, copied here by `make build` / the Dockerfile.
// Only .gitkeep is committed, so the package compiles before the web app has been built.
//
//go:embed all:web
var webFS embed.FS

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	port := env("PORT", "8080")

	cfg, err := game.ConfigFromEnv(os.Getenv)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	botAfter, err := secondsEnv("DEV_BOT_HUMAN_AFTER_SEC")
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	var st *store.Store
	if url := os.Getenv("DATABASE_URL"); url != "" {
		var err error
		st, err = store.Open(ctx, url)
		if err != nil {
			slog.Error("database", "err", err)
			os.Exit(1)
		}
		defer st.Close()
		if err := st.Migrate(ctx); err != nil {
			slog.Error("migrate", "err", err)
			os.Exit(1)
		}
		if n, err := st.AbortRunningGames(ctx, time.Now()); err != nil {
			slog.Error("abort stale games", "err", err)
		} else if n > 0 {
			slog.Info("marked games from the previous run as aborted", "games", n)
		}
	} else {
		slog.Warn("DATABASE_URL not set: running without accounts")
	}

	var authStore auth.Store // keep the interfaces nil (not a typed nil) when there is no DB
	var gameStore hub.Store
	if st != nil {
		authStore, gameStore = st, st
	}
	authSvc := auth.New(authStore)

	games := hub.New(ctx, hub.Options{
		Config:        cfg,
		BotHumanAfter: botAfter,
		Agent:         agent.NewStub(uint64(time.Now().UnixNano())),
		Store:         gameStore,
	})
	slog.Info("game config",
		"humans", cfg.HumansPerGame, "ai", cfg.AIPerGame, "rounds", cfg.Rounds,
		"t_intro", cfg.TIntro, "t_ask", cfg.TAsk, "t_answer", cfg.TAnswer, "t_reveal", cfg.TReveal, "t_vote", cfg.TVote,
		"dev_bot_human_after", botAfter)
	if botAfter > 0 {
		slog.Warn("DEV_BOT_HUMAN_AFTER_SEC is set: lone players get a bot_human partner (dev only)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthHandler(st))
	authSvc.Routes(mux)
	mux.Handle("GET /ws", games.Handler(authSvc.UserID, listEnv("WS_ORIGINS")))
	mux.HandleFunc("GET /api/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(cfg.Public())
	})

	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		slog.Error("embed", "err", err)
		os.Exit(1)
	}
	mux.Handle("/", staticHandler(webRoot))

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		// No WriteTimeout: WebSocket connections will live on this server.
	}

	go func() {
		slog.Info("listening", "addr", srv.Addr, "db", st != nil)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// secondsEnv reads a whole number of seconds; unset or 0 means off.
func secondsEnv(key string) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, errors.New(key + " must be a whole number of seconds")
	}
	return time.Duration(n) * time.Second, nil
}

// listEnv reads a comma-separated list, e.g. WS_ORIGINS=localhost:3000 for `next dev`.
func listEnv(key string) []string {
	var out []string
	for _, p := range strings.Split(os.Getenv(key), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// healthHandler is a liveness check: it always answers 200 while the process runs, and reports
// the database state in the body so it is easy to see from the deployed URL.
func healthHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		db := "disabled"
		if st != nil {
			ctx, cancel := context.WithTimeout(r.Context(), time.Second)
			defer cancel()
			if err := st.Ping(ctx); err != nil {
				db = "down"
			} else {
				db = "ok"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "db": db})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// staticHandler serves the Next.js export. It resolves /login to login.html (or login/index.html)
// and falls back to the export's 404.html. Unknown /api paths never get HTML.
func staticHandler(root fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(root, "index.html"); err != nil {
			http.Error(w, "Web UI not built. Run `make build`.", http.StatusServiceUnavailable)
			return
		}

		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		target := ""
		for _, cand := range []string{p, p + ".html", path.Join(p, "index.html")} {
			if p == "" {
				cand = "index.html"
			}
			if info, err := fs.Stat(root, cand); err == nil && !info.IsDir() {
				target = cand
				break
			}
		}
		if target == "" {
			w.Header().Set("Cache-Control", "no-store")
			if page, err := fs.ReadFile(root, "404.html"); err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write(page)
				return
			}
			http.NotFound(w, r)
			return
		}

		if strings.HasPrefix(target, "_next/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFileFS(w, r, root, target)
	})
}
