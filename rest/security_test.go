package rest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestValidationMessagesRemainJSONData(t *testing.T) {
	// Quoting in validation messages is presentation, never a query or HTML
	// template. Preserve the public message contract and verify the HTTP sink.
	payload := `'"><script>alert(1)</script>\`
	issues := ValidateDict(context.Background(), Schema{
		"tags": {Type: TypeList, Allowed: []any{"draft"}},
	}, map[string]any{"tags": []any{payload}}, false)
	issues["unique"] = "value '" + payload + "' is not unique"
	response := httptest.NewRecorder()
	WriteError(response, httptest.NewRequest(http.MethodPatch, "/items/id", nil), &ValidationError{Issues: issues})
	if response.Code != http.StatusUnprocessableEntity || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("validation response: %d %v", response.Code, response.Header())
	}
	var body struct {
		Issues Issues `json:"_issues"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(body.Issues, issues) {
		t.Fatalf("message data changed in JSON response: %v", body.Issues)
	}
}
