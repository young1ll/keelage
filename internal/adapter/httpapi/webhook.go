package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// WebhookSink receives verified forge deliveries.
type WebhookSink interface {
	HandleDelivery(ctx context.Context, event, delivery string, body []byte) (any, error)
}

// VerifySignature checks GitHub's X-Hub-Signature-256 over the raw body.
func VerifySignature(secret string, body []byte, header string) bool {
	sig, ok := strings.CutPrefix(header, "sha256=")
	if !ok || secret == "" {
		return false
	}
	want, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(m.Sum(nil), want)
}

// MountWebhooks adds POST /webhooks/github (spec §4.6). Deliveries are
// accepted only with a valid signature; the sink's answer is echoed.
func MountWebhooks(r chi.Router, secret string, sink WebhookSink) {
	r.Post("/webhooks/github", func(w http.ResponseWriter, req *http.Request) {
		if secret == "" || sink == nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("github webhooks are not configured"))
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 10<<20))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if !VerifySignature(secret, body, req.Header.Get("X-Hub-Signature-256")) {
			writeErr(w, http.StatusUnauthorized, errors.New("bad webhook signature"))
			return
		}
		event := req.Header.Get("X-GitHub-Event")
		if event == "ping" {
			writeJSON(w, http.StatusOK, map[string]string{"pong": req.Header.Get("X-GitHub-Delivery")})
			return
		}
		res, err := sink.HandleDelivery(req.Context(), event, req.Header.Get("X-GitHub-Delivery"), body)
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, res)
	})
}
