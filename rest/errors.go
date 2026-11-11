package rest

import (
	"context"
	"errors"
	"net/http"
)

// Error is an error with the HTTP status Eve would answer. Hooks return it to
// abort a request, like flask.abort in the Python APIs.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return http.StatusText(e.Status)
	}
	return e.Message
}

// Abort builds an Error; an empty message is filled with Werkzeug's default
// for the status when the response is written.
func Abort(status int, message string) *Error {
	return &Error{Status: status, Message: message}
}

// Errors returned by Service.
var (
	ErrNotFound             = Abort(http.StatusNotFound, "")
	ErrPreconditionRequired = Abort(http.StatusPreconditionRequired, msgIfMatchRequired)
)

// PreconditionFailedError is returned when If-Match does not match. It keeps
// the current document so the response can tell who changed it.
type PreconditionFailedError struct {
	Current Doc
}

func (e *PreconditionFailedError) Error() string { return msgEtagMismatch }

// ValidationError carries Cerberus-like issues of one document.
type ValidationError struct {
	Issues Issues
}

func (e *ValidationError) Error() string { return "validation failed" }

// InsertError is returned by Service.Insert when some documents are invalid;
// Results has one entry per document, nil for the valid ones.
type InsertError struct {
	Results []Issues
	Failed  int
}

func (e *InsertError) Error() string { return "insertion failure" }

// Messages are Werkzeug's default error descriptions, which Eve returns in
// _error.message. Werkzeug 0.x (Eve 0.7 APIs) used two spaces between
// sentences.
type Messages map[int]string

var (
	// Werkzeug1 is the text of Werkzeug >= 1.0 (Eve 1.x and 2.x).
	Werkzeug1 = Messages{
		http.StatusBadRequest:          msgBadRequest,
		http.StatusNotFound:            msgNotFound,
		http.StatusMethodNotAllowed:    msgMethodNotAllowed,
		http.StatusConflict:            msgConflict,
		http.StatusInternalServerError: msgInternal,
	}
	// Werkzeug0 is the text of Werkzeug 0.11 (Eve 0.7).
	Werkzeug0 = Messages{
		http.StatusBadRequest:          msgBadRequest,
		http.StatusNotFound:            "The requested URL was not found on the server.  If you entered the URL manually please check your spelling and try again.",
		http.StatusMethodNotAllowed:    msgMethodNotAllowed,
		http.StatusConflict:            "A conflict happened while processing the request.  The resource might have been modified while the request was being processed.",
		http.StatusInternalServerError: "The server encountered an internal error and was unable to complete your request.  Either the server is overloaded or there is an error in the application.",
	}
)

func (m Messages) message(status int) string {
	if msg, ok := m[status]; ok {
		return msg
	}
	return http.StatusText(status)
}

type messagesKey struct{}

func messagesFrom(ctx context.Context) Messages {
	if m, ok := ctx.Value(messagesKey{}).(Messages); ok {
		return m
	}
	return Werkzeug1
}

// IsNotFound reports whether err means the document does not exist.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
