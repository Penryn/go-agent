package napcat

import (
	"context"
	"log/slog"
	"time"

	napcatsdk "github.com/zjutjh/napcat-sdk"
)

type WSReceiver struct {
	url         string
	accessToken string
	options     []napcatsdk.Option
}

func NewWSReceiver(url, accessToken string, options ...napcatsdk.Option) *WSReceiver {
	return &WSReceiver{
		url:         url,
		accessToken: accessToken,
		options:     append([]napcatsdk.Option(nil), options...),
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

		connected, err := r.receiveOnce(ctx, handler)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if connected {
			backoff = minBackoff
			slog.Warn("ws: connection lost", "url", r.url, "error", err, "reconnect_in", backoff)
		} else {
			slog.Warn("ws: dial failed", "url", r.url, "error", err, "retry_in", backoff)
		}
		if err := waitContext(ctx, backoff); err != nil {
			return err
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (r *WSReceiver) receiveOnce(ctx context.Context, handler func(context.Context, []byte) error) (bool, error) {
	options := []napcatsdk.Option{
		napcatsdk.WithToken(r.accessToken),
		napcatsdk.WithEventBuffer(1024),
		napcatsdk.WithEventDeliveryTimeout(time.Second),
	}
	options = append(options, r.options...)

	client, err := napcatsdk.DialWebSocket(ctx, r.url, options...)
	if err != nil {
		return false, err
	}
	slog.Info("ws: connected", "url", r.url)

	for {
		select {
		case <-ctx.Done():
			go func() { _ = client.Close() }()
			return true, ctx.Err()
		case event, ok := <-client.Events():
			if !ok {
				_ = client.Close()
				return true, client.Err()
			}
			if event == nil {
				continue
			}
			if err := handler(ctx, event.Raw()); err != nil {
				slog.Error("ws: handler error", "error", err)
			}
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
