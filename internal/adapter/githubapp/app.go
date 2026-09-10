// Package githubapp is the GitHub App adapter (spec §4.5): it authenticates
// as the app (RS256 JWT → installation token), parses webhook deliveries
// into port.RepoEvent and writes PR comments and check runs. VERSION lists
// the verified contract and the check date.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/young1ll/keelage/internal/port"
)

// App is one GitHub App installation client.
type App struct {
	AppID      string
	PrivateKey *rsa.PrivateKey
	// APIBase defaults to https://api.github.com (GHES: https://host/api/v3).
	APIBase string
	HTTP    *http.Client
	Now     func() time.Time

	mu     sync.Mutex
	tokens map[int64]installToken
}

type installToken struct {
	token   string
	expires time.Time
}

// ParsePrivateKey reads the PEM the app settings page hands out.
func ParsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("githubapp: no PEM block in private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: private key: %w", err)
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("githubapp: private key is not RSA")
	}
	return rk, nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) base() string {
	if a.APIBase != "" {
		return strings.TrimRight(a.APIBase, "/")
	}
	return "https://api.github.com"
}

func (a *App) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// JWT signs the app's bearer token (10 minutes maximum; 60 s of drift).
func (a *App) JWT() (string, error) {
	if a.AppID == "" || a.PrivateKey == nil {
		return "", errors.New("githubapp: app id and private key required")
	}
	now := a.now().Unix()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iat": now - 60, "exp": now + 9*60, "iss": a.AppID})
	signing := b64(header) + "." + b64(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.PrivateKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signing + "." + b64(sig), nil
}

func (a *App) do(ctx context.Context, method, path, bearer string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("githubapp: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(raw) > 0 {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// InstallationToken returns a cached or fresh installation access token.
func (a *App) InstallationToken(ctx context.Context, installation int64) (string, error) {
	a.mu.Lock()
	if t, ok := a.tokens[installation]; ok && a.now().Add(2*time.Minute).Before(t.expires) {
		a.mu.Unlock()
		return t.token, nil
	}
	a.mu.Unlock()
	jwt, err := a.JWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if _, err := a.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(installation, 10)+"/access_tokens", jwt, map[string]any{}, &out); err != nil {
		return "", err
	}
	a.mu.Lock()
	if a.tokens == nil {
		a.tokens = map[int64]installToken{}
	}
	a.tokens[installation] = installToken{token: out.Token, expires: out.ExpiresAt}
	a.mu.Unlock()
	return out.Token, nil
}

// UpsertComment implements port.PRReporter.
func (a *App) UpsertComment(ctx context.Context, installation int64, owner, repo string, number int, marker, body string) error {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	var existing []struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if _, err := a.do(ctx, http.MethodGet, base+"?per_page=100", tok, nil, &existing); err != nil {
		return err
	}
	for _, c := range existing {
		if strings.Contains(c.Body, marker) {
			_, err := a.do(ctx, http.MethodPatch, fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, c.ID), tok, map[string]string{"body": body}, nil)
			return err
		}
	}
	_, err = a.do(ctx, http.MethodPost, base, tok, map[string]string{"body": body}, nil)
	return err
}

// Check implements port.PRReporter.
func (a *App) Check(ctx context.Context, installation int64, owner, repo, headSHA, name, conclusion, title, summary string) error {
	tok, err := a.InstallationToken(ctx, installation)
	if err != nil {
		return err
	}
	in := map[string]any{
		"name": name, "head_sha": headSHA, "status": "completed", "conclusion": conclusion,
		"completed_at": a.now().UTC().Format(time.RFC3339),
		"output":       map[string]string{"title": title, "summary": summary},
	}
	_, err = a.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/check-runs", owner, repo), tok, in, nil)
	return err
}

// Parse turns a webhook delivery into a RepoEvent. Unknown events and
// actions come back with Kind "ignored".
func Parse(event, delivery string, body []byte) (port.RepoEvent, error) {
	ev := port.RepoEvent{Kind: "ignored", Delivery: delivery}
	switch event {
	case "installation":
		var p struct {
			Action       string `json:"action"`
			Installation struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installation"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		if p.Action != "created" {
			return ev, nil
		}
		ev.Kind, ev.Installation, ev.Account = "installation", p.Installation.ID, p.Installation.Account.Login
		return ev, nil
	case "pull_request", "pull_request_review":
		var p struct {
			Action       string `json:"action"`
			Number       int    `json:"number"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Repository struct {
				Name  string `json:"name"`
				Owner struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repository"`
			PullRequest struct {
				Number int    `json:"number"`
				Title  string `json:"title"`
				URL    string `json:"html_url"`
				Head   struct {
					SHA string `json:"sha"`
				} `json:"head"`
			} `json:"pull_request"`
			Review struct {
				ID          int64     `json:"id"`
				State       string    `json:"state"`
				Body        string    `json:"body"`
				SubmittedAt time.Time `json:"submitted_at"`
				User        struct {
					Login string `json:"login"`
				} `json:"user"`
			} `json:"review"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			return ev, err
		}
		ev.Installation, ev.Owner, ev.Repo = p.Installation.ID, p.Repository.Owner.Login, p.Repository.Name
		ev.Number, ev.HeadSHA, ev.Title, ev.URL = p.PullRequest.Number, p.PullRequest.Head.SHA, p.PullRequest.Title, p.PullRequest.URL
		if ev.Number == 0 {
			ev.Number = p.Number
		}
		switch {
		case event == "pull_request" && p.Action == "opened", event == "pull_request" && p.Action == "reopened":
			ev.Kind = "pr_opened"
		case event == "pull_request" && p.Action == "synchronize":
			ev.Kind = "pr_synchronize"
		case event == "pull_request_review" && p.Action == "submitted":
			ev.Kind = "pr_review"
			ev.Review = &port.ReviewInfo{ID: p.Review.ID, State: strings.ToLower(p.Review.State), Body: p.Review.Body, Login: p.Review.User.Login, SubmittedAt: p.Review.SubmittedAt}
		}
		return ev, nil
	}
	return ev, nil
}

var _ port.PRReporter = (*App)(nil)
