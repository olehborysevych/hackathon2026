// Package store holds the Postgres queries and embedded SQL migrations (SPEC §5, §6).
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	ErrNotFound      = errors.New("not found")
	ErrUsernameTaken = errors.New("username taken")
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
}

type Store struct {
	pool *pgxpool.Pool
}

// Open connects to Postgres, retrying for a while because on a fresh deploy the database
// service may come up a few seconds after the app does.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	var lastErr error
	for attempt := 1; attempt <= 15; attempt++ {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		lastErr = pool.Ping(pctx)
		cancel()
		if lastErr == nil {
			return &Store{pool: pool}, nil
		}
		slog.Warn("database not ready", "attempt", attempt, "err", lastErr)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pool.Close()
	return nil, fmt.Errorf("connect to database: %w", lastErr)
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies embedded migrations in filename order. Applied versions are recorded in
// schema_migrations; an advisory lock keeps two starting processes from racing.
func (s *Store) Migrate(ctx context.Context) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	const lockID = 7263901 // arbitrary app-wide constant
	if _, err := conn.Exec(ctx, `select pg_advisory_lock($1)`, lockID); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `select pg_advisory_unlock($1)`, lockID) //nolint:errcheck

	if _, err := conn.Exec(ctx, `create table if not exists schema_migrations (
		version text primary key, applied_at timestamptz not null default now())`); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var done bool
		if err := conn.QueryRow(ctx, `select exists(select 1 from schema_migrations where version=$1)`, name).Scan(&done); err != nil {
			return err
		}
		if done {
			continue
		}
		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `insert into schema_migrations(version) values($1)`, name); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("applied migration", "version", name)
	}
	return nil
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, consentAt time.Time) (User, error) {
	u := User{Username: username, PasswordHash: passwordHash}
	err := s.pool.QueryRow(ctx,
		`insert into users(username, password_hash, consent_at) values($1,$2,$3) returning id`,
		username, passwordHash, consentAt).Scan(&u.ID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrUsernameTaken
	}
	return u, err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`select id, username, password_hash from users where username=$1`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, expires time.Time) error {
	// Opportunistic cleanup keeps the table small without a background job.
	if _, err := s.pool.Exec(ctx, `delete from sessions where expires_at < now()`); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`insert into sessions(token_hash, user_id, expires_at) values($1,$2,$3)`, tokenHash, userID, expires)
	return err
}

func (s *Store) UserBySession(ctx context.Context, tokenHash string, now time.Time) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		`select u.id, u.username, u.password_hash from sessions s join users u on u.id = s.user_id
		 where s.token_hash=$1 and s.expires_at > $2`, tokenHash, now).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.pool.Exec(ctx, `delete from sessions where token_hash=$1`, tokenHash)
	return err
}
