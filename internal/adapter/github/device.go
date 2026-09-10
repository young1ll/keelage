// Package github is the identity provider adapter: the OAuth device flow
// (VERSION lists the documented contract and the check date). The server
// never stores the GitHub token; it only reads the login once.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/young1ll/keelage/internal/port"
)

// Device implements port.OAuthDevice.
type Device struct {
	ClientID string
	// OAuthBase is https://github.com; APIBase https://api.github.com.
	// Both are overridable for GitHub Enterprise and tests.
	OAuthBase string
	APIBase   string
	HTTP      *http.Client
}

const grantType = "urn:ietf:params:oauth:grant-type:device_code"

func (d *Device) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (d *Device) oauth() string {
	if d.OAuthBase != "" {
		return strings.TrimRight(d.OAuthBase, "/")
	}
	return "https://github.com"
}

func (d *Device) api() string {
	if d.APIBase != "" {
		return strings.TrimRight(d.APIBase, "/")
	}
	return "https://api.github.com"
}

func (d *Device) postForm(ctx context.Context, u string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("github: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// Start implements port.OAuthDevice.
func (d *Device) Start(ctx context.Context) (port.DeviceStart, error) {
	if d.ClientID == "" {
		return port.DeviceStart{}, errors.New("github: client id not configured")
	}
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
	}
	if err := d.postForm(ctx, d.oauth()+"/login/device/code", url.Values{"client_id": {d.ClientID}, "scope": {"read:user"}}, &out); err != nil {
		return port.DeviceStart{}, err
	}
	if out.Error != "" {
		return port.DeviceStart{}, fmt.Errorf("github: %s", out.Error)
	}
	return port.DeviceStart{DeviceCode: out.DeviceCode, UserCode: out.UserCode, VerificationURI: out.VerificationURI, ExpiresIn: out.ExpiresIn, Interval: out.Interval}, nil
}

// Poll implements port.OAuthDevice.
func (d *Device) Poll(ctx context.Context, deviceCode string) (string, string, int, error) {
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Interval    int    `json:"interval"`
	}
	form := url.Values{"client_id": {d.ClientID}, "device_code": {deviceCode}, "grant_type": {grantType}}
	if err := d.postForm(ctx, d.oauth()+"/login/oauth/access_token", form, &out); err != nil {
		return "", "", 0, err
	}
	switch out.Error {
	case "":
	case "authorization_pending":
		return "pending", "", out.Interval, nil
	case "slow_down":
		return "slow_down", "", out.Interval, nil
	case "expired_token":
		return "expired", "", 0, nil
	case "access_denied":
		return "denied", "", 0, nil
	default:
		return "", "", 0, fmt.Errorf("github: %s", out.Error)
	}
	login, err := d.login(ctx, out.AccessToken)
	if err != nil {
		return "", "", 0, err
	}
	return "ok", login, 0, nil
}

func (d *Device) login(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.api()+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := d.client().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github: /user: %s", resp.Status)
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", errors.New("github: /user returned no login")
	}
	return u.Login, nil
}

var _ port.OAuthDevice = (*Device)(nil)
