package qq

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

var sharedMediaClient = MediaClient()

// MediaClient only fetches public HTTP images; group input cannot access local services.
func MediaClient() *http.Client {
	transport := &http.Transport{ResponseHeaderTimeout: 4 * time.Second, MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, errors.New("private media address is not allowed")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: 4 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many media redirects")
		}
		return MediaURL(req.URL.String())
	}}
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}
func MediaURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return errors.New("media must use a public HTTP URL")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicIP(ip) {
		return errors.New("private media address is not allowed")
	}
	return nil
}
func FetchImage(ctx context.Context, client *http.Client, raw string, maxBytes int64) ([]byte, string, error) {
	if err := MediaURL(raw); err != nil {
		return nil, "", err
	}
	if client == nil {
		client = sharedMediaClient
	}
	if maxBytes <= 0 {
		maxBytes = 10 << 20
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, "", fmt.Errorf("media HTTP %d", response.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > maxBytes {
		return nil, "", errors.New("image exceeds size limit")
	}
	typ := http.DetectContentType(b)
	switch typ {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, "", errors.New("unsupported image format")
	}
	if typ != "image/webp" {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16000000 {
			return nil, "", errors.New("invalid or oversized image dimensions")
		}
	}
	return b, typ, nil
}
func ImageData(b []byte, mime string) (string, error) {
	if mime == "image/gif" {
		img, err := gif.Decode(bytes.NewReader(b))
		if err != nil {
			return "", err
		}
		var out bytes.Buffer
		if err := png.Encode(&out, img); err != nil {
			return "", err
		}
		b = out.Bytes()
		mime = "image/png"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b), nil
}
