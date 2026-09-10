package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recSink struct {
	event, delivery string
	body            []byte
}

func (s *recSink) HandleDelivery(_ context.Context, event, delivery string, body []byte) (any, error) {
	s.event, s.delivery, s.body = event, delivery, body
	return map[string]string{"kind": "ok"}, nil
}

func sign(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestWebhook_SignatureAndDispatch(t *testing.T) {
	sink := &recSink{}
	r := New("keelage-server", "test")
	MountWebhooks(r, "s3cret", sink)
	body := []byte(`{"action":"opened","number":1}`)
	post := func(sig, event string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("X-GitHub-Event", event)
		req.Header.Set("X-GitHub-Delivery", "d-1")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := post("", "pull_request"); rec.Code != 401 {
		t.Fatalf("no signature: %d", rec.Code)
	}
	if rec := post(sign("wrong", body), "pull_request"); rec.Code != 401 {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	if rec := post("sha256=zz", "pull_request"); rec.Code != 401 {
		t.Fatalf("bad hex: %d", rec.Code)
	}
	if rec := post(sign("s3cret", body), "ping"); rec.Code != 200 {
		t.Fatalf("ping: %d", rec.Code)
	}
	rec := post(sign("s3cret", body), "pull_request")
	if rec.Code != 202 || sink.event != "pull_request" || sink.delivery != "d-1" || !bytes.Equal(sink.body, body) {
		t.Fatalf("dispatch: %d %+v", rec.Code, sink)
	}
	// unconfigured: refused, never processed
	r2 := New("keelage-server", "test")
	MountWebhooks(r2, "", sink)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	rec2 := httptest.NewRecorder()
	r2.ServeHTTP(rec2, req)
	if rec2.Code != 503 {
		t.Fatalf("unconfigured: %d", rec2.Code)
	}
}
