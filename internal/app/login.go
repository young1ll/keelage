package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/young1ll/keelage/internal/port"
)

// Login is the device login (spec §4.2: OIDC, GitHub first): the CLI shows
// the provider's user code, the person approves in a browser, and the
// server issues a keelage token to members of the org. The server keeps no
// state between the two steps; the provider's device code travels with the
// client.
type Login struct {
	OAuth   port.OAuthDevice
	Members port.Membership
	Tokens  port.TokenIssuer
	// TTL of issued tokens (0: no expiry).
	TTL time.Duration
}

// ErrNotMember: the provider identified the user, but the org does not list them.
var ErrNotMember = errors.New("not a member of the org")

// Start begins a device login.
func (l *Login) Start(ctx context.Context) (port.DeviceStart, error) {
	if l.OAuth == nil {
		return port.DeviceStart{}, errors.New("login: no identity provider configured")
	}
	return l.OAuth.Start(ctx)
}

// Poll completes a device login once the provider reports approval.
func (l *Login) Poll(ctx context.Context, org, deviceCode string) (port.DevicePoll, error) {
	if l.OAuth == nil {
		return port.DevicePoll{}, errors.New("login: no identity provider configured")
	}
	if org == "" || deviceCode == "" {
		return port.DevicePoll{}, errors.New("login: org and device_code required")
	}
	status, login, interval, err := l.OAuth.Poll(ctx, deviceCode)
	if err != nil {
		return port.DevicePoll{}, err
	}
	if status != "ok" {
		return port.DevicePoll{Status: status, Interval: interval}, nil
	}
	ok, err := l.Members.IsMember(ctx, org, login)
	if err != nil {
		return port.DevicePoll{}, err
	}
	if !ok {
		return port.DevicePoll{}, fmt.Errorf("%w: %s in %s", ErrNotMember, login, org)
	}
	tok, err := l.Tokens.IssueToken(ctx, org, login, "human", "", "device-login", l.TTL)
	if err != nil {
		return port.DevicePoll{}, err
	}
	return port.DevicePoll{Status: "ok", Token: tok, User: login, Org: org}, nil
}
