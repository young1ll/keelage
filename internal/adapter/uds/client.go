package uds

import (
	"context"
	"net"
	"net/http"
	"time"
)

// BaseURL is the host placeholder used in requests over the socket.
const BaseURL = "http://keelage"

// NewClient returns an HTTP client that dials the Unix socket. timeout is
// the whole-request budget (dial included): the hook path uses 50 ms and
// fails open on expiry.
func NewClient(path string, timeout time.Duration) *http.Client {
	d := net.Dialer{Timeout: timeout}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return d.DialContext(ctx, "unix", path)
			},
			DisableKeepAlives: true,
		},
	}
}
