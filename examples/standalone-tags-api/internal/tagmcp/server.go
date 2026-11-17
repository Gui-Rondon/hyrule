// Package tagmcp adapts the local tags pilot to MCP without adding MCP imports
// to taglookup or the framework. It supports a single explicitly owned local
// credential source, not a multi-user network authentication boundary.
package tagmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"example.com/standalone-tags-api/internal/taglookup"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const ProtocolVersion = "2025-11-25"
const MaxFrameBytes = 64 * 1024

type Config struct {
	Service  *rest.Service
	Identity *taglookup.JWTIdentity
	Scope    string
	Audit    func(context.Context, taglookup.Record) error
	// Token returns an untrusted compact JWT for this server's local owner.
	// It must honor context and be concurrency-safe. Never derive it from MCP
	// arguments, client metadata or shared state belonging to another owner.
	Token func(context.Context) (string, error)
}

// NewServer creates an opt-in SDK server; it starts no transport. Use one server
// per local owner. Authorization and audit are repeated, never cached per session.
func NewServer(c Config) (*mcp.Server, error) {
	if c.Identity == nil || c.Token == nil {
		return nil, errors.New("tagmcp: configured identity and owner credential source are required")
	}
	executor, err := taglookup.New(taglookup.Dependencies{Service: c.Service, Scope: c.Scope, Verify: c.Identity.Verify, Authorize: c.Identity.Authorize, Audit: c.Audit})
	if err != nil {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "hyrule-tags-pilot", Version: taglookup.Version}, &mcp.ServerOptions{
		SupportedProtocolVersions: []string{ProtocolVersion},
		Capabilities:              &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	// SDK middleware is the dynamic tools projection: no second static registry or
	// schema inference can expose a tool before Discover authorizes the request.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			if method != "tools/list" && method != "tools/call" {
				switch method {
				case "initialize", "ping", "notifications/initialized", "notifications/cancelled":
					return next(ctx, method, request)
				}
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "unsupported method"}
			}
			ctx, cancel := context.WithTimeout(ctx, taglookup.ExecutionTimeout)
			defer cancel()
			token, tokenErr := c.Token(ctx)
			if tokenErr != nil {
				token = ""
			}
			ctx = taglookup.WithToken(ctx, token)
			if method == "tools/list" {
				if params, ok := request.GetParams().(*mcp.ListToolsParams); ok && params != nil && params.Cursor != "" {
					_, err := executor.Execute(ctx, strings.NewReader(""))
					if !errors.Is(err, taglookup.ErrInput) || errors.Is(err, taglookup.ErrAudit) {
						return nil, protocolError(err)
					}
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "invalid cursor"}
				}
				contract, err := executor.Discover(ctx)
				if err != nil {
					return nil, protocolError(err)
				}
				closed := false
				return &mcp.ListToolsResult{Tools: []*mcp.Tool{{
					Name: contract.Name, Description: contract.Description, InputSchema: contract.InputSchema, OutputSchema: contract.OutputSchema,
					Annotations: &mcp.ToolAnnotations{ReadOnlyHint: contract.ReadOnly, OpenWorldHint: &closed},
					Meta:        mcp.Meta{"hyrule": map[string]any{"version": contract.Version, "contractHash": contract.Hash, "limits": contract.Limits, "serverRules": contract.ServerRules}},
				}}}, nil
			}
			params, ok := request.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || params == nil || params.Name != taglookup.Name {
				// Unknown tools still traverse identity/policy/audit, without storage.
				_, err := executor.Execute(ctx, strings.NewReader(""))
				if !errors.Is(err, taglookup.ErrInput) || errors.Is(err, taglookup.ErrAudit) {
					return nil, protocolError(err)
				}
				return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "unknown tool"}
			}
			raw := params.Arguments
			if len(params.InputResponses) > 0 || params.RequestState != "" {
				raw = nil
			}
			output, err := executor.Execute(ctx, bytes.NewReader(raw))
			if err != nil {
				if !errors.Is(err, taglookup.ErrAudit) && (errors.Is(err, taglookup.ErrIdentity) || errors.Is(err, taglookup.ErrForbidden)) {
					return nil, protocolError(err)
				}
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: safeMessage(err)}}}, nil
			}
			return &mcp.CallToolResult{StructuredContent: json.RawMessage(output), Content: []mcp.Content{&mcp.TextContent{Text: string(output)}}}, nil
		}
	})
	return server, nil
}

// RunStdio reserves stdout for SDK protocol frames. It grants no identity merely
// because stdin is local. Application logs must go to stderr, and the caller owns
// resource/audit shutdown. The frame cap is separate from the 1 KiB tool input cap.
func RunStdio(ctx context.Context, c Config) error {
	server, err := NewServer(c)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{MaxLineLength: MaxFrameBytes})
}
func protocolError(err error) error {
	code := jsonrpc.CodeInternalError
	switch {
	case errors.Is(err, taglookup.ErrAudit):
	case errors.Is(err, taglookup.ErrIdentity):
		code = -32001
	case errors.Is(err, taglookup.ErrForbidden):
		code = -32003
	case errors.Is(err, taglookup.ErrInput):
		code = jsonrpc.CodeInvalidParams
	}
	return &jsonrpc.Error{Code: int64(code), Message: safeMessage(err)}
}
func safeMessage(err error) string {
	switch {
	case errors.Is(err, taglookup.ErrAudit):
		return "audit unavailable"
	case errors.Is(err, taglookup.ErrIdentity):
		return "unauthenticated"
	case errors.Is(err, taglookup.ErrForbidden):
		return "forbidden"
	case errors.Is(err, taglookup.ErrInput):
		return "invalid input"
	case errors.Is(err, taglookup.ErrDeadline):
		return "deadline exceeded"
	case errors.Is(err, taglookup.ErrCanceled):
		return "canceled"
	case errors.Is(err, taglookup.ErrOutput):
		return "invalid output"
	default:
		return "lookup unavailable"
	}
}
