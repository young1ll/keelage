package mcpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTools(t *testing.T) {
	ctx := context.Background()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/what_touches":
			_ = json.NewEncoder(w).Encode(map[string]any{"anchor": r.URL.Query().Get("anchor"), "repo": r.URL.Query().Get("repo"), "state": "verified", "constraints": []any{}})
		case "/v1/related":
			if r.URL.Query().Get("id") == "nope" {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "related: unknown id nope"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": r.URL.Query().Get("id"), "kind": "constraint"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer daemon.Close()

	s := New(daemon.Client(), daemon.URL, "myrepo", "test")
	ct, st := mcp.NewInMemoryTransports()
	go func() { _ = s.MCP().Run(ctx, st) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	tools, err := sess.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 2 {
		t.Fatalf("tools: %v %v", err, tools)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "what_touches", Arguments: map[string]any{"anchor": "code://a.ts#f"}})
	if err != nil {
		t.Fatal(err)
	}
	out := res.Content[0].(*mcp.TextContent).Text
	if res.IsError || !strings.Contains(out, `"repo": "myrepo"`) || !strings.Contains(out, "code://a.ts#f") {
		t.Fatalf("what_touches: %v %s", res.IsError, out)
	}
	res, err = sess.CallTool(ctx, &mcp.CallToolParams{Name: "related", Arguments: map[string]any{"id": "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "unknown id") {
		t.Fatalf("daemon errors surface as tool errors: %v %+v", res.IsError, res.Content)
	}
	// daemon down → tool error with guidance, not a crash
	dead := New(&http.Client{}, "http://127.0.0.1:1", "r", "test")
	ct2, st2 := mcp.NewInMemoryTransports()
	go func() { _ = dead.MCP().Run(ctx, st2) }()
	sess2, _ := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct2, nil)
	defer func() { _ = sess2.Close() }()
	res, _ = sess2.CallTool(ctx, &mcp.CallToolParams{Name: "related", Arguments: map[string]any{"id": "c1"}})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "keelage daemon") {
		t.Fatalf("dead daemon: %+v", res)
	}
}
