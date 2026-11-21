package hub

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"hackathon2026/server/internal/ws"
)

// Authenticator returns the signed-in user's id for a request (the `sid` cookie), or an error.
type Authenticator func(r *http.Request) (userID string, err error)

const (
	maxFrame     = 8 << 10
	pingInterval = 25 * time.Second // keeps proxies (Railway's edge) from closing idle sockets
	writeTimeout = 10 * time.Second
)

// Handler serves /ws. Upgrades are accepted only from the page's own origin plus any extra
// host patterns (e.g. "localhost:3000" for `next dev`), and only for signed-in users.
func (h *Hub) Handler(authn Authenticator, extraOrigins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := authn(r)
		if err != nil || userID == "" {
			http.Error(w, "not signed in", http.StatusUnauthorized)
			return
		}
		// Accept rejects a cross-origin upgrade with 403 unless the origin's host matches r.Host
		// or one of OriginPatterns.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: extraOrigins})
		if err != nil {
			slog.Info("ws accept", "err", err)
			return
		}
		conn.SetReadLimit(maxFrame)
		c := h.Connect(userID)
		ctx, cancel := context.WithCancel(h.ctx)
		defer cancel()
		defer h.Disconnect(c)

		go h.writer(ctx, cancel, conn, c)

		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				if s := websocket.CloseStatus(err); s != websocket.StatusNormalClosure && s != websocket.StatusGoingAway && !errors.Is(err, context.Canceled) {
					slog.Debug("ws read", "err", err)
				}
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			var env ws.Envelope
			if err := json.Unmarshal(data, &env); err != nil || env.Type == "" {
				h.sendError(c, "bad_request", "expected {type, payload}")
				continue
			}
			h.Handle(c, env)
		}
	})
}

func (h *Hub) writer(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, c *Client) {
	defer cancel()
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.Closed():
			conn.Close(websocket.StatusPolicyViolation, "too slow")
			return
		case frame := <-c.Send:
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, frame)
			wcancel()
			if err != nil {
				return
			}
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
