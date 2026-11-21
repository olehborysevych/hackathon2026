// Command export writes one JSON line per finished game (seats, messages, votes, notes, results,
// ai_calls) to stdout. It reads the real_games view, so games with bot_human seats are never
// exported (CLAUDE.md invariant 5).
//
//	DATABASE_URL=... go run ./server/cmd/export > games.jsonl
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"hackathon2026/server/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "export:", err)
		os.Exit(1)
	}
}

func run() error {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	st, err := store.Open(ctx, url)
	if err != nil {
		return err
	}
	defer st.Close()

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	enc := json.NewEncoder(w)
	n := 0
	err = st.ExportGames(ctx, func(g store.ExportGame) error {
		n++
		return enc.Encode(g)
	})
	fmt.Fprintf(os.Stderr, "exported %d games\n", n)
	return err
}
