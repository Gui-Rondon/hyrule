package evecompat

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// FieldType is the subset of Cerberus types used by the Eve APIs. An empty
// type accepts any value.
type FieldType string

const (
	TypeString   FieldType = "string"
	TypeBoolean  FieldType = "boolean"
	TypeInteger  FieldType = "integer"
	TypeNumber   FieldType = "number"
	TypeDict     FieldType = "dict"
	TypeList     FieldType = "list"
	TypeObjectID FieldType = "objectid"
	TypeDatetime FieldType = "datetime"
)

// Field is a Cerberus field definition. The messages follow Cerberus 1.3.
type Field struct {
	Type     FieldType
	Required bool
	Nullable bool
	Unique   bool
	Default  any
	// Allowed lists the accepted values; for lists, every element.
	Allowed []any
	// MinLength and MaxLength apply to strings and lists (0 = unchecked).
	MinLength int
	MaxLength int
	// Min and Max apply to numbers.
	Min, Max *float64
	// NotEmpty is Cerberus' empty: False.
	NotEmpty bool
	// AllowEmpty is an explicit empty: True: an empty value skips the
	// remaining rules (Cerberus 1.3 _validate_empty drops minlength, regex,
	// allowed...). Without it, "" is still checked against Regex.
	AllowEmpty bool
	Regex      *regexp.Regexp

	// Schema validates the keys of a dict.
	Schema Schema
	// AllowUnknown accepts keys outside Schema in a dict.
	AllowUnknown bool
	// Items validates every element of a list.
	Items *Field

	// Check runs after the other rules (Cerberus' check_with or a custom
	// type). doc is the document being validated (the changes on PATCH, as
	// Cerberus' self.document). It returns nil or the issue: a message or
	// nested Issues.
	Check func(ctx context.Context, value any, doc map[string]any) any
}

// Schema maps field names to definitions.
type Schema map[string]Field

// Issues maps a field to its error message (or nested Issues), as Eve's
// _issues.
type Issues map[string]any

type validator struct {
	schema       Schema
	allowUnknown bool
	store        Store
	custom       func(ctx context.Context, doc, original Doc, partial bool) (Issues, error)
}

// validate checks doc in place: JSON values are converted to their BSON types
// and, on full validation (POST, PUT), defaults are applied. selfID excludes
// the document being updated from the unique checks.
func (v validator) validate(ctx context.Context, doc map[string]any, partial bool, original Doc) (Issues, error) {
	var selfID any
	if original != nil {
		selfID = original["_id"]
	}
	issues := validateDict(ctx, v.schema, doc, partial, v.allowUnknown)
	if v.custom != nil {
		extra, err := v.custom(ctx, doc, original, partial)
		if err != nil {
			return nil, err
		}
		for k, msg := range extra {
			if _, ok := issues[k]; !ok {
				issues[k] = msg
			}
		}
	}
	// Cerberus runs every rule, so unique errors come with the type errors
	unique, err := v.checkUnique(ctx, doc, selfID, issues)
	if err != nil {
		return nil, err
	}
	for k, msg := range unique {
		issues[k] = msg
	}
	if len(issues) > 0 {
		return issues, nil
	}
	if !partial {
		applyDefaults(v.schema, doc)
	} else if original != nil {
		applyPatchDefaults(v.schema, doc, original)
	}
	return nil, nil
}

func validateDict(ctx context.Context, schema Schema, doc map[string]any, partial, allowUnknown bool) Issues {
	issues := Issues{}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field, ok := schema[key]
		if !ok {
			if !allowUnknown {
				issues[key] = "unknown field"
			}
			continue
		}
		if issue := validateValue(ctx, field, doc[key], doc); issue != nil {
			issues[key] = issue
		} else if converted, ok := convert(field, doc[key]); ok {
			doc[key] = converted
		}
	}
	if partial {
		return issues
	}
	for key, field := range schema {
		if _, ok := doc[key]; !ok && field.Required && field.Default == nil {
			issues[key] = "required field"
		}
	}
	return issues
}

// validateValue returns nil when valid, a message, or the nested Issues of a
// dict or list ({"headline": {"text": "must be of string type"}}).
func validateValue(ctx context.Context, field Field, value any, doc map[string]any) any {
	if value == nil {
		if field.Nullable {
			return nil
		}
		return "null value not allowed"
	}
	if msg := checkType(field, value); msg != "" {
		return msg
	}
	if msg := checkRules(field, value); msg != "" {
		return msg
	}
	switch field.Type {
	case TypeDict:
		if field.Schema != nil {
			if nested := validateDict(ctx, field.Schema, value.(map[string]any), false, field.AllowUnknown); len(nested) > 0 {
				return nested
			}
		}
	case TypeList:
		if field.Items != nil {
			nested := Issues{}
			for i, item := range value.([]any) {
				if issue := validateValue(ctx, *field.Items, item, doc); issue != nil {
					nested[fmt.Sprint(i)] = issue
				}
			}
			if len(nested) > 0 {
				return nested
			}
		}
	}
	if field.Check != nil {
		return field.Check(ctx, value, doc)
	}
	return nil
}

func checkType(field Field, value any) string {
	switch field.Type {
	case TypeString:
		if _, ok := value.(string); !ok {
			return "must be of string type"
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			return "must be of boolean type"
		}
	case TypeInteger:
		if f, ok := value.(float64); !ok || f != math.Trunc(f) {
			return "must be of integer type"
		}
	case TypeNumber:
		if _, ok := value.(float64); !ok {
			return "must be of number type"
		}
	case TypeObjectID:
		s, ok := value.(string)
		if !ok || !objectIDRe.MatchString(s) {
			return fmt.Sprintf("value '%v' cannot be converted to a ObjectId", value)
		}
	case TypeDatetime:
		s, ok := value.(string)
		if !ok {
			return "must be of datetime type"
		}
		if _, err := parseDate(s); err != nil {
			return "must be of datetime type"
		}
	case TypeDict:
		if _, ok := value.(map[string]any); !ok {
			return "must be of dict type"
		}
	case TypeList:
		if _, ok := value.([]any); !ok {
			return "must be of list type"
		}
	}
	return ""
}

func checkRules(field Field, value any) string {
	if field.NotEmpty && isEmpty(value) {
		return "empty values not allowed"
	}
	if field.AllowEmpty && isEmpty(value) {
		return ""
	}
	if n, ok := length(value); ok {
		if field.MinLength > 0 && n < field.MinLength {
			return fmt.Sprintf("min length is %d", field.MinLength)
		}
		if field.MaxLength > 0 && n > field.MaxLength {
			return fmt.Sprintf("max length is %d", field.MaxLength)
		}
	}
	if f, ok := value.(float64); ok {
		if field.Min != nil && f < *field.Min {
			return "min value is " + pyNumber(*field.Min)
		}
		if field.Max != nil && f > *field.Max {
			return "max value is " + pyNumber(*field.Max)
		}
	}
	if field.Regex != nil {
		if s, ok := value.(string); ok && !field.Regex.MatchString(s) {
			return fmt.Sprintf("value does not match regex '%s'", field.Regex.String())
		}
	}
	if len(field.Allowed) > 0 {
		if list, ok := value.([]any); ok {
			var bad []string
			for _, item := range list {
				if !slices.Contains(field.Allowed, item) {
					bad = append(bad, "'"+pyRepr(item)+"'")
				}
			}
			if len(bad) > 0 {
				return fmt.Sprintf("unallowed values [%s]", strings.Join(bad, ", "))
			}
		} else if !slices.Contains(field.Allowed, value) {
			return "unallowed value " + pyRepr(value)
		}
	}
	return ""
}

func isEmpty(value any) bool {
	n, ok := length(value)
	return ok && n == 0
}

func length(value any) (int, bool) {
	switch t := value.(type) {
	case string:
		return utf8.RuneCountInString(t), true
	case []any:
		return len(t), true
	case map[string]any:
		return len(t), true
	}
	return 0, false
}

// convert turns validated JSON values into their BSON representation.
func convert(field Field, value any) (any, bool) {
	switch field.Type {
	case TypeInteger:
		if f, ok := value.(float64); ok {
			return int64(f), true
		}
	case TypeObjectID:
		if s, ok := value.(string); ok {
			oid, err := bson.ObjectIDFromHex(s)
			return oid, err == nil
		}
	case TypeDatetime:
		if s, ok := value.(string); ok {
			t, err := parseDate(s)
			return t, err == nil
		}
	case TypeList:
		if list, ok := value.([]any); ok && field.Items != nil {
			for i, item := range list {
				if c, ok := convert(*field.Items, item); ok {
					list[i] = c
				}
			}
		}
	case TypeDict:
		if m, ok := value.(map[string]any); ok && field.Schema != nil {
			for k, f := range field.Schema {
				if c, ok := convert(f, m[k]); ok && m[k] != nil {
					m[k] = c
				}
			}
		}
	}
	return value, false
}

func applyDefaults(schema Schema, doc map[string]any) {
	for key, field := range schema {
		if _, ok := doc[key]; !ok && field.Default != nil {
			doc[key] = field.Default
		}
		if sub, ok := doc[key].(map[string]any); ok && field.Schema != nil {
			applyDefaults(field.Schema, sub)
		}
	}
}

// applyPatchDefaults is Eve 2.x's NORMALIZE_ON_PATCH (_normalize_default):
// a PATCH gets the default of a field that is neither sent nor stored. In a
// sent dict the stored dict is the reference (Eve 2.3.1 looks the nested key
// up at the top level of the stored document and can reset it).
func applyPatchDefaults(schema Schema, doc, stored map[string]any) {
	for key, field := range schema {
		value, sent := doc[key]
		_, has := stored[key]
		if !sent && !has && field.Default != nil {
			doc[key] = field.Default
		}
		sub, ok := value.(map[string]any)
		if !ok || field.Schema == nil {
			continue
		}
		storedSub, _ := asMap(stored[key])
		applyPatchDefaults(field.Schema, sub, storedSub)
	}
}

// checkUnique skips the fields that already failed validation.
func (v validator) checkUnique(ctx context.Context, doc map[string]any, selfID any, failed Issues) (Issues, error) {
	issues := Issues{}
	for key, field := range v.schema {
		value, ok := doc[key]
		if _, bad := failed[key]; !field.Unique || !ok || bad || value == nil {
			continue
		}
		filter := bson.M{key: value}
		if selfID != nil {
			filter["_id"] = bson.M{"$ne": selfID}
		}
		n, err := v.store.CountDocuments(ctx, filter)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			issues[key] = fmt.Sprintf("value '%s' is not unique", pyRepr(value))
		}
	}
	return issues, nil
}

// pyRepr renders a value as str() would in Cerberus messages.
func pyRepr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return pyNumber(t)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func pyNumber(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprint(f)
}

// ValidateDict validates a nested document against schema; custom checks use
// it for schemas chosen at runtime (e.g. vars templates).
func ValidateDict(ctx context.Context, schema Schema, doc map[string]any, allowUnknown bool) Issues {
	return validateDict(ctx, schema, doc, false, allowUnknown)
}

// ValidateDictPartial is ValidateDict for a PATCH: required fields are not
// enforced.
func ValidateDictPartial(ctx context.Context, schema Schema, doc map[string]any, allowUnknown bool) Issues {
	return validateDict(ctx, schema, doc, true, allowUnknown)
}
