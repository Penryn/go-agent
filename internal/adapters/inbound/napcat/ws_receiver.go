package napcat

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// pongWait is the maximum time to wait for a Pong (or any read) before
	// considering the connection dead. NapCat does not send application-level
	// heartbeats, so we must rely on WebSocket-level Ping/Pong.
	pongWait = 90 * time.Second

	// pingInterval is how often we send a Ping frame to the peer.
	// Must be strictly less than pongWait so the read deadline is refreshed
	// before it expires during idle periods.
	pingInterval = 30 * time.Second

	// maxMessageSize is the maximum incoming WebSocket message size (16 MiB).
	maxMessageSize = 16 << 20
)

type WSReceiver struct {
	url         string
	accessToken string
	dialer      *websocket.Dialer
}

func NewWSReceiver(url, accessToken string, dialer *websocket.Dialer) *WSReceiver {
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	return &WSReceiver{
		url:         url,
		accessToken: accessToken,
		dialer:      dialer,
	}
}

func (r *WSReceiver) Receive(ctx context.Context, handler func(context.Context, []byte) error) error {
	const (
		minBackoff = time.Second
		maxBackoff = 30 * time.Second
	)

	backoff := minBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		conn, _, err := r.dial(ctx)
		if err != nil {
			slog.Warn("ws: dial failed", "url", r.url, "error", err, "retry_in", backoff)
			if waitErr := waitContext(ctx, backoff); waitErr != nil {
				return waitErr
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		backoff = minBackoff
		slog.Info("ws: connected", "url", r.url)
		err = r.readLoop(ctx, conn, handler)
		_ = conn.Close()
		if err == nil || errors.Is(err, context.Canceled) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		slog.Warn("ws: connection lost", "error", err, "reconnect_in", backoff)
		if waitErr := waitContext(ctx, backoff); waitErr != nil {
			return waitErr
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (r *WSReceiver) dial(ctx context.Context) (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	if r.accessToken != "" {
		header.Set("Authorization", "Bearer "+r.accessToken)
		header.Set("X-Access-Token", r.accessToken)
	}
	return r.dialer.DialContext(ctx, r.url, header)
}

func (r *WSReceiver) readLoop(ctx context.Context, conn *websocket.Conn, handler func(context.Context, []byte) error) error {
	closeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeDone:
		}
	}()
	defer close(closeDone)

	conn.SetReadLimit(maxMessageSize)
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))

	// Periodic Ping writer: sends a Ping frame every pingInterval so the
	// peer responds with Pong, which resets the read deadline. Without this
	// goroutine the connection would always time out after pongWait of
	// silence because no Pong would ever arrive.
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					slog.Debug("ws: ping write failed", "error", err)
					return
				}
			case <-ctx.Done():
				return
			case <-closeDone:
				return
			}
		}
	}()

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}
		if err := handler(ctx, payload); err != nil {
			slog.Error("ws: handler error", "error", err)
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
