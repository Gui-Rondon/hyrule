package taglookup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Gui-Rondon/hyrule/rest"
)

const HTTPPath = "/tools/tags.lookup"

// NewHTTPHandler binds the pilot to verified JWT identity and mandatory audit.
// GET describes the tool; POST executes it. No route is mounted automatically.
// Use NewLocalHTTPServer (or equivalent read/write timeouts) to bound body reads.
func NewHTTPHandler(service *rest.Service, identity *JWTIdentity, audit func(context.Context, Record) error) (http.Handler, error) {
	if identity == nil || len(identity.keys) == 0 {
		return nil, errors.New("taglookup: HTTP requires configured JWT identity")
	}
	e, err := New(Dependencies{Service: service, Scope: identity.scope, Verify: identity.Verify, Authorize: identity.Authorize, Audit: audit})
	if err != nil {
		return nil, err
	}
	return &httpHandler{executor: e}, nil
}

type httpHandler struct{ executor *Executor }

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Authorization")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path != HTTPPath {
		writeHTTPError(w, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeHTTPError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	// Overwrite inherited token context even when this request has no credentials.
	// Cookies, query strings and auth.WithClaims are never credential sources.
	ctx := WithToken(r.Context(), bearer(r))
	inputStatus := 0
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		inputStatus = http.StatusBadRequest
	}
	if r.Method == http.MethodGet {
		if r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			inputStatus = http.StatusBadRequest
		}
		if inputStatus == 0 {
			tool, err := h.executor.Describe(ctx)
			if err != nil {
				writeExecutorError(w, err, 0)
				return
			}
			data, _ := json.Marshal(struct {
				Name     string `json:"name"`
				Version  string `json:"version"`
				ReadOnly bool   `json:"read_only"`
			}{tool.Name, tool.Version, tool.ReadOnly})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
	} else {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" {
			inputStatus = http.StatusUnsupportedMediaType
		}
		if r.ContentLength > MaxInput {
			inputStatus = http.StatusRequestEntityTooLarge
		}
	}
	var input io.Reader = strings.NewReader("")
	var observed *httpInput
	if inputStatus == 0 {
		observed = &httpInput{reader: http.MaxBytesReader(w, r.Body, MaxInput)}
		input = observed
	}
	// Invalid transport inputs still pass through identity, policy and audit before
	// returning their validation failure, never reaching resource storage.
	output, err := h.executor.Execute(ctx, input)
	if err != nil {
		if observed != nil {
			var sizeErr *http.MaxBytesError
			var networkErr net.Error
			if errors.As(observed.err, &sizeErr) {
				inputStatus = http.StatusRequestEntityTooLarge
			} else if errors.As(observed.err, &networkErr) && networkErr.Timeout() {
				inputStatus = http.StatusRequestTimeout
			}
		}
		writeExecutorError(w, err, inputStatus)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output)
}

type httpInput struct {
	reader io.Reader
	err    error
}

func (i *httpInput) Read(p []byte) (int, error) {
	n, err := i.reader.Read(p)
	if err != nil {
		i.err = err
	}
	return n, err
}
func bearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || len(token) > MaxToken || strings.ContainsAny(token, " \t\r\n") {
		return ""
	}
	return token
}
func writeExecutorError(w http.ResponseWriter, err error, inputStatus int) {
	switch {
	case errors.Is(err, ErrAudit):
		writeHTTPError(w, http.StatusServiceUnavailable, "audit_unavailable")
	case errors.Is(err, ErrIdentity):
		w.Header().Set("WWW-Authenticate", `Bearer realm="tags.lookup"`)
		writeHTTPError(w, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, ErrForbidden):
		writeHTTPError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrDeadline):
		writeHTTPError(w, http.StatusGatewayTimeout, "deadline_exceeded")
	case errors.Is(err, ErrCanceled):
		writeHTTPError(w, http.StatusRequestTimeout, "canceled")
	case errors.Is(err, ErrInput):
		if inputStatus == 0 {
			inputStatus = http.StatusBadRequest
		}
		code := "invalid_input"
		if inputStatus == http.StatusRequestEntityTooLarge {
			code = "payload_too_large"
		} else if inputStatus == http.StatusUnsupportedMediaType {
			code = "unsupported_media_type"
		} else if inputStatus == http.StatusRequestTimeout {
			code = "request_timeout"
		}
		writeHTTPError(w, inputStatus, code)
	case errors.Is(err, ErrOutput):
		writeHTTPError(w, http.StatusInternalServerError, "invalid_output")
	default:
		writeHTTPError(w, http.StatusServiceUnavailable, "lookup_unavailable")
	}
}
func writeHTTPError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Error string `json:"error"`
	}{code})
}

// NewLocalHTTPServer creates, but does not start, a server bound to an explicit
// numeric loopback address. Read/write budgets include slow clients and audit.
// This pilot is not a remote deployment, CORS integration or an MCP transport.
func NewLocalHTTPServer(address string, handler http.Handler) (*http.Server, error) {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	number, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || number < 0 || number > 65535 || handler == nil {
		return nil, errors.New("taglookup: explicit loopback address and handler are required")
	}
	return &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: executionTimeout, ReadTimeout: executionTimeout, WriteTimeout: executionTimeout + auditTimeout + 2*time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 * 1024}, nil
}
