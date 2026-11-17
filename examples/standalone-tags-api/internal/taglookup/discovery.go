package taglookup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

const schemaDialect = "https://json-schema.org/draft/2020-12/schema"

// Contract is an authorized, operation-specific projection of the registered
// resource's scalar types, not a second CRUD schema or a public framework API.
// Every call allocates fresh metadata. No records or credentials are included.
type Contract struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	ReadOnly     bool           `json:"read_only"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
	Limits       ContractLimits `json:"limits"`
	ServerRules  []string       `json:"server_rules"`
	Hash         string         `json:"contract_hash,omitempty"`
}
type ContractLimits struct {
	InputBytes      int   `json:"input_bytes"`
	OutputBytes     int   `json:"output_bytes"`
	Results         int   `json:"results"`
	ExecutionMillis int64 `json:"execution_timeout_ms"`
	AuditMillis     int64 `json:"audit_timeout_ms"`
}

// Discover repeats authorization and mandatory audit through Describe. Neither
// structural schemas nor the contract hash grant permission to invoke the tool.
func (e *Executor) Discover(ctx context.Context) (Contract, error) {
	tool, err := e.Describe(ctx)
	if err != nil {
		return Contract{}, err
	}
	schema := e.deps.Service.Resource().Schema
	slug := func() map[string]any {
		return map[string]any{
			"type": string(schema["slug"].Type), "minLength": minSlugLength, "maxLength": maxSlugLength,
			// A negative character class needs no regex anchors, whose end-of-string
			// semantics differ between Go and JavaScript for a final newline.
			"not": map[string]any{"pattern": invalidSlugPattern},
		}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		fields := make([]any, len(required))
		for i, name := range required {
			fields[i] = name
		}
		return map[string]any{"type": "object", "properties": properties, "required": fields, "additionalProperties": false}
	}
	input := object(map[string]any{"slug": slug()}, "slug")
	input["$schema"] = schemaDialect
	tag := object(map[string]any{
		"name":    map[string]any{"type": string(schema["name"].Type)},
		"slug":    slug(),
		"enabled": map[string]any{"type": string(schema["enabled"].Type), "const": true},
	}, "name", "slug", "enabled")
	contract := Contract{
		Name: tool.Name, Version: tool.Version, ReadOnly: tool.ReadOnly,
		Description: "Look up one enabled tag by exact, case-sensitive slug.",
		InputSchema: input,
		OutputSchema: map[string]any{"$schema": schemaDialect, "oneOf": []any{
			object(map[string]any{"found": map[string]any{"const": false}}, "found"),
			object(map[string]any{"found": map[string]any{"const": true}, "tag": tag}, "found", "tag"),
		}},
		Limits: ContractLimits{InputBytes: MaxInput, OutputBytes: MaxOutput, Results: 1, ExecutionMillis: executionTimeout.Milliseconds(), AuditMillis: auditTimeout.Milliseconds()},
		ServerRules: []string{
			"Verified identity and tags:read permission for the bound resource are required for discovery and invocation.",
			"Duplicate JSON keys, oversized byte payloads and invalid transport metadata are rejected by the server.",
			"The server enforces enabled-only lookup, mandatory datasource filters and exact equality with the requested slug.",
			"Fetched hooks run on the server; inconsistent or oversized output is rejected.",
			"Mandatory audit must commit before any descriptor or lookup result is released.",
		},
	}
	// All values come from bounded static metadata or validated scalar type names.
	canonical, err := json.Marshal(contract)
	if err != nil {
		return Contract{}, ErrOutput
	}
	sum := sha256.Sum256(canonical)
	contract.Hash = hex.EncodeToString(sum[:])
	return contract, nil
}
