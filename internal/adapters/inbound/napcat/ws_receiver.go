package napcat

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
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
			log.Printf("napcat ws: dial %s failed: %v (retry in %s)", r.url, err, backoff)
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
		log.Printf("napcat ws: connected to %s", r.url)
		err = r.readLoop(ctx, conn, handler)
		_ = conn.Close()
		if err == nil || errors.Is(err, context.Canceled) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		log.Printf("napcat ws: connection lost: %v (reconnect in %s)", err, backoff)
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

	conn.SetReadLimit(16 << 20)
	conn.SetPongHandler(func(_ string) error {
		return conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	})
	_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))

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
			log.Printf("napcat ws handler error: %v", err)
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
