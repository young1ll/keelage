package githubapp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGitHub checks the app JWT, hands out installation tokens and records
// comment and check-run calls.
type fakeGitHub struct {
	pub      *rsa.PublicKey
	tokens   int
	comments []map[string]string
	patched  []string
	checks   []map[string]any
}

func (f *fakeGitHub) verifyJWT(t *testing.T, h string) {
	t.Helper()
	tok, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		t.Fatalf("no bearer: %q", h)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt parts: %d", len(parts))
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	if err := rsa.VerifyPKCS1v15(f.pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("jwt signature: %v", err)
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	claims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct {
		Iat, Exp int64
		Iss      string
	}
	_ = json.Unmarshal(claims, &c)
	if !strings.Contains(string(hdr), `"alg":"RS256"`) || c.Iss != "4242" || c.Exp-c.Iat > 600 {
		t.Fatalf("jwt header/claims: %s %+v", hdr, c)
	}
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app/installations/7/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		f.verifyJWT(t, r.Header.Get("Authorization"))
		f.tokens++
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour)})
	})
	auth := func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghs_x" {
			t.Fatalf("installation token missing: %q", r.Header.Get("Authorization"))
		}
	}
	mux.HandleFunc("GET /repos/acme/api/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		out := []map[string]any{{"id": 1, "body": "unrelated"}}
		for i, c := range f.comments {
			out = append(out, map[string]any{"id": 100 + i, "body": c["body"]})
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST /repos/acme/api/issues/12/comments", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.comments = append(f.comments, in)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":100}`))
	})
	mux.HandleFunc("PATCH /repos/acme/api/issues/comments/100", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.patched = append(f.patched, in["body"])
		_, _ = w.Write([]byte(`{"id":100}`))
	})
	mux.HandleFunc("POST /repos/acme/api/check-runs", func(w http.ResponseWriter, r *http.Request) {
		auth(r)
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.checks = append(f.checks, in)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":5}`))
	})
	return mux
}

func TestApp_TokenCommentUpsertAndCheck(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	priv, err := ParsePrivateKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	gh := &fakeGitHub{pub: &key.PublicKey}
	srv := httptest.NewServer(gh.handler(t))
	defer srv.Close()
	app := &App{AppID: "4242", PrivateKey: priv, APIBase: srv.URL}
	ctx := context.Background()

	if err := app.UpsertComment(ctx, 7, "acme", "api", 12, "<!-- k -->", "<!-- k -->\nfirst"); err != nil {
		t.Fatal(err)
	}
	if err := app.UpsertComment(ctx, 7, "acme", "api", 12, "<!-- k -->", "<!-- k -->\nsecond"); err != nil {
		t.Fatal(err)
	}
	if len(gh.comments) != 1 || len(gh.patched) != 1 || gh.patched[0] != "<!-- k -->\nsecond" {
		t.Fatalf("upsert: posted %d patched %v", len(gh.comments), gh.patched)
	}
	if err := app.Check(ctx, 7, "acme", "api", "abc", "keelage", "neutral", "judgment pending", "body"); err != nil {
		t.Fatal(err)
	}
	if len(gh.checks) != 1 || gh.checks[0]["head_sha"] != "abc" || gh.checks[0]["conclusion"] != "neutral" || gh.checks[0]["status"] != "completed" {
		t.Fatalf("check: %+v", gh.checks)
	}
	if gh.tokens != 1 {
		t.Fatalf("installation token fetched %d times (cache)", gh.tokens)
	}
}

func TestParse_Deliveries(t *testing.T) {
	pr := `{"action":"synchronize","number":12,"installation":{"id":7},"repository":{"name":"api","owner":{"login":"acme"}},
	        "pull_request":{"number":12,"title":"t","html_url":"u","head":{"sha":"abc"}}}`
	ev, err := Parse("pull_request", "d1", []byte(pr))
	if err != nil || ev.Kind != "pr_synchronize" || ev.Installation != 7 || ev.Owner != "acme" || ev.Repo != "api" || ev.Number != 12 || ev.HeadSHA != "abc" {
		t.Fatalf("pr: %+v %v", ev, err)
	}
	rv := `{"action":"submitted","installation":{"id":7},"repository":{"name":"api","owner":{"login":"acme"}},
	        "pull_request":{"number":12,"head":{"sha":"abc"}},
	        "review":{"id":55,"state":"APPROVED","body":"ok","submitted_at":"2026-09-10T10:00:00Z","user":{"login":"alice"}}}`
	ev, err = Parse("pull_request_review", "d2", []byte(rv))
	if err != nil || ev.Kind != "pr_review" || ev.Review == nil || ev.Review.State != "approved" || ev.Review.Login != "alice" || ev.Review.ID != 55 {
		t.Fatalf("review: %+v %v", ev, err)
	}
	ev, _ = Parse("installation", "d3", []byte(`{"action":"created","installation":{"id":7,"account":{"login":"acme"}}}`))
	if ev.Kind != "installation" || ev.Account != "acme" || ev.Installation != 7 {
		t.Fatalf("installation: %+v", ev)
	}
	if ev, _ := Parse("pull_request", "d4", []byte(`{"action":"labeled"}`)); ev.Kind != "ignored" {
		t.Fatalf("labeled: %+v", ev)
	}
	if ev, _ := Parse("push", "d5", []byte(`{}`)); ev.Kind != "ignored" {
		t.Fatalf("push: %+v", ev)
	}
	if _, err := Parse("pull_request", "d6", []byte(`nope`)); err == nil {
		t.Fatal("bad json accepted")
	}
}
