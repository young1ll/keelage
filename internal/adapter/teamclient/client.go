// Package teamclient is the daemon's HTTP client for keelage-server: it
// implements port.TeamClient (sync, gates) plus the login and proof calls
// the CLI uses. Errors carry the server's status and message.
package teamclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/core"
	"github.com/young1ll/keelage/internal/core/accountability"
	"github.com/young1ll/keelage/internal/merkle"
	"github.com/young1ll/keelage/internal/port"
)

// Client talks to one server with one token.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// New builds a client.
func New(base, token string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Error is a non-2xx answer.
type Error struct {
	Status int
	Code   string
	Reason string
	Msg    string
}

func (e *Error) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("server: %d %s: %s", e.Status, e.Code, e.Reason)
	}
	return fmt.Sprintf("server: %d: %s", e.Status, e.Msg)
}

// Rejection returns the refusal as a core rejection when the server
// refused a command by rule.
func (e *Error) Rejection() (*core.Rejection, bool) {
	if e.Code == "" {
		return nil, false
	}
	return &core.Rejection{Code: e.Code, Reason: e.Reason}, true
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		e := &Error{Status: resp.StatusCode}
		var body struct {
			Error  string `json:"error"`
			Code   string `json:"code"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(raw, &body) == nil {
			e.Code, e.Reason, e.Msg = body.Code, body.Reason, body.Error
		} else {
			e.Msg = strings.TrimSpace(string(raw))
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %w", port.ErrUnauthenticated, e)
		case http.StatusForbidden:
			return fmt.Errorf("%w: %w", port.ErrForbidden, e)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", port.ErrNotFound, e)
		}
		return e
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Push implements port.TeamClient.
func (c *Client) Push(ctx context.Context, req port.PushRequest) (port.PushResponse, error) {
	var out port.PushResponse
	err := c.do(ctx, http.MethodPost, "/sync/push", req, &out)
	return out, err
}

// Pull implements port.TeamClient.
func (c *Client) Pull(ctx context.Context, since int64, limit int) (port.PullResponse, error) {
	var out port.PullResponse
	q := url.Values{"since": {strconv.FormatInt(since, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	err := c.do(ctx, http.MethodGet, "/sync/pull?"+q.Encode(), nil, &out)
	return out, err
}

// RegisterDaemon implements port.TeamClient.
func (c *Client) RegisterDaemon(ctx context.Context, id, publicKey string) error {
	return c.do(ctx, http.MethodPost, "/sync/register", map[string]string{"daemon_id": id, "public_key": publicKey}, nil)
}

// RequestGate implements port.TeamClient.
func (c *Client) RequestGate(ctx context.Context, req port.GateRequest) (accountability.Gate, error) {
	var out accountability.Gate
	err := c.do(ctx, http.MethodPost, "/gates", req, &out)
	return out, err
}

// ResolveGate implements port.TeamClient.
func (c *Client) ResolveGate(ctx context.Context, id core.ID, res port.GateResolution) (accountability.Gate, error) {
	var out accountability.Gate
	err := c.do(ctx, http.MethodPost, "/gates/"+url.PathEscape(string(id))+"/resolve", res, &out)
	return out, err
}

// ListGates implements port.TeamClient.
func (c *Client) ListGates(ctx context.Context, state accountability.GateState) ([]accountability.Gate, error) {
	var out []accountability.Gate
	err := c.do(ctx, http.MethodGet, "/gates?state="+url.QueryEscape(string(state)), nil, &out)
	return out, err
}

// Me returns the principal the token resolves to.
func (c *Client) Me(ctx context.Context) (port.Principal, error) {
	var out port.Principal
	err := c.do(ctx, http.MethodGet, "/me", nil, &out)
	return out, err
}

// PublicKey returns the server's checkpoint key.
func (c *Client) PublicKey(ctx context.Context) (string, error) {
	var out struct {
		PublicKey string `json:"public_key"`
	}
	err := c.do(ctx, http.MethodGet, "/ledger/key", nil, &out)
	return out.PublicKey, err
}

// DeviceStart begins a device login.
func (c *Client) DeviceStart(ctx context.Context) (port.DeviceStart, error) {
	var out port.DeviceStart
	err := c.do(ctx, http.MethodPost, "/auth/device/start", map[string]string{}, &out)
	return out, err
}

// DevicePoll polls a device login for org.
func (c *Client) DevicePoll(ctx context.Context, org, deviceCode string) (port.DevicePoll, error) {
	var out port.DevicePoll
	err := c.do(ctx, http.MethodPost, "/auth/device/poll", map[string]string{"org": org, "device_code": deviceCode}, &out)
	return out, err
}

// Checkpoint returns the latest signed tree head.
func (c *Client) Checkpoint(ctx context.Context) (merkle.Checkpoint, error) {
	var out merkle.Checkpoint
	err := c.do(ctx, http.MethodGet, "/ledger/checkpoint", nil, &out)
	return out, err
}

// Inclusion returns the inclusion bundle for a server seq.
func (c *Client) Inclusion(ctx context.Context, seq int64) (merkle.Bundle, error) {
	var out merkle.Bundle
	err := c.do(ctx, http.MethodGet, "/ledger/proof/inclusion?seq="+strconv.FormatInt(seq, 10), nil, &out)
	return out, err
}

// Consistency returns the consistency bundle between two tree sizes.
func (c *Client) Consistency(ctx context.Context, from, to uint64) (merkle.ConsistencyBundle, error) {
	var out merkle.ConsistencyBundle
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/ledger/proof/consistency?from=%d&to=%d", from, to), nil, &out)
	return out, err
}

// ErrNoServer: the daemon is not logged in to a server.
var ErrNoServer = errors.New("not logged in to a server (keelage server login)")

var _ port.TeamClient = (*Client)(nil)
