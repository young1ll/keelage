// Package mcpsrv is keelage's local MCP server: a thin stdio client of the
// daemon's Unix-socket API (patterns §5: the daemon is the single truth).
// Tools: what_touches(anchor) and related(id) — the hookless path (§3.5b).
package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WhatTouchesInput is the what_touches tool input.
type WhatTouchesInput struct {
	Anchor string `json:"anchor" jsonschema:"canonical anchor to look up, e.g. code://src/billing/fee.ts#calcFee or code://src/billing/fee.ts"`
	Repo   string `json:"repo,omitempty" jsonschema:"repository id (base name of the repository root); defaults to the project directory"`
}

// RelatedInput is the related tool input.
type RelatedInput struct {
	ID string `json:"id" jsonschema:"a constraint id or a canonical anchor"`
}

// Server wraps the MCP server and the daemon client.
type Server struct {
	srv         *mcp.Server
	client      *http.Client
	base        string
	defaultRepo string
}

// New builds the server. base is the daemon's base URL for client.
func New(client *http.Client, base, defaultRepo, version string) *Server {
	s := &Server{client: client, base: base, defaultRepo: defaultRepo}
	s.srv = mcp.NewServer(&mcp.Implementation{Name: "keelage", Version: version}, nil)
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "what_touches",
		Description: "Constraints and decisions bound to a realization anchor, and the anchor's staleness state. Call before editing the symbol or file.",
	}, s.whatTouches)
	mcp.AddTool(s.srv, &mcp.Tool{
		Name:        "related",
		Description: "For a constraint id: the anchors it binds and its supersedes chain. For an anchor: the constraints bound to it and where it moved.",
	}, s.related)
	return s
}

// Run serves over stdio until ctx is done or stdin closes.
func (s *Server) Run(ctx context.Context) error { return s.srv.Run(ctx, &mcp.StdioTransport{}) }

// MCP exposes the underlying server (tests connect it to an in-memory transport).
func (s *Server) MCP() *mcp.Server { return s.srv }

func (s *Server) get(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("keelage daemon is not reachable (%w); start it with `keelage daemon`", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return nil, errors.New(e.Error)
	}
	return body, nil
}

func text(body json.RawMessage) *mcp.CallToolResult {
	var pretty map[string]any
	if err := json.Unmarshal(body, &pretty); err == nil {
		if b, err := json.MarshalIndent(pretty, "", "  "); err == nil {
			body = b
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(body)}}}
}

func (s *Server) whatTouches(ctx context.Context, _ *mcp.CallToolRequest, in WhatTouchesInput) (*mcp.CallToolResult, any, error) {
	repo := in.Repo
	if repo == "" {
		repo = s.defaultRepo
	}
	body, err := s.get(ctx, "/v1/what_touches", url.Values{"anchor": {in.Anchor}, "repo": {repo}})
	if err != nil {
		return nil, nil, err
	}
	return text(body), nil, nil
}

func (s *Server) related(ctx context.Context, _ *mcp.CallToolRequest, in RelatedInput) (*mcp.CallToolResult, any, error) {
	body, err := s.get(ctx, "/v1/related", url.Values{"id": {in.ID}})
	if err != nil {
		return nil, nil, err
	}
	return text(body), nil, nil
}
