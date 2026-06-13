package media

import (
	"context"
	"net"
	"net/url"
	"strings"
)

type contextKey int

const publicEndpointKey contextKey = 1

// WithPublicEndpoint attaches a client-reachable S3/MinIO base URL to ctx (dev).
func WithPublicEndpoint(ctx context.Context, endpoint string) context.Context {
	if endpoint == "" {
		return ctx
	}
	return context.WithValue(ctx, publicEndpointKey, endpoint)
}

func publicEndpointFromContext(ctx context.Context, fallback string) string {
	if v, ok := ctx.Value(publicEndpointKey).(string); ok && v != "" {
		return v
	}
	return fallback
}

// AllowedDevMediaEndpoint gates client-supplied MinIO hosts in development.
func AllowedDevMediaEndpoint(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return false
	}

	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "10.0.2.2" {
		return true
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsPrivate() || ip.IsLoopback()
}
