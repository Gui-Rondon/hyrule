package evecompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// DateFormat is Eve's DATE_FORMAT, used in responses and accepted in where.
const DateFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// DefaultQueryDenylist blocks the operators that run JavaScript on the server.
// Eve's default MONGO_QUERY_BLACKLIST also blocks $regex; resources that kept
// that default must add it to Resource.QueryDenylist.
var DefaultQueryDenylist = []string{"$where", "$function", "$accumulator"}

// ListQuery is the parsed form of Eve's query string.
type ListQuery struct {
	Filter     bson.M
	Sort       bson.D
	Projection bson.M
	Page       int
	MaxResults int
	// ShowDeleted lists soft deleted documents too: Eve checks the presence
	// of show_deleted, not its value.
	ShowDeleted bool

	// raw values, echoed in the pagination links like Eve does
	rawWhere string
	rawSort  string
	other    url.Values
}

var errBadRequest = errors.New("bad request")

// ErrDeniedOperator is returned when where uses a denied operator.
type ErrDeniedOperator struct{ Operator string }

func (e ErrDeniedOperator) Error() string {
	return fmt.Sprintf("query contains denied operator %s", e.Operator)
}

// ParseListQuery parses where, sort, projection, page and max_results with
// Eve's rules (eve.utils.parse_request): an invalid or non positive
// max_results falls back to the default, max_results is capped at the limit,
// page uses its absolute value and an invalid page is ignored. With
// pagination disabled the default is 0 (no limit), there is no cap and page
// is not read.
func ParseListQuery(args url.Values, p Pagination, denylist []string) (*ListQuery, error) {
	return parseListQuery(args, p, denylist, false)
}

func parseListQuery(args url.Values, p Pagination, denylist []string, idsAsString bool) (*ListQuery, error) {
	def := p.Default
	if p.Disabled {
		def = 0
	}
	q := &ListQuery{Page: 1, MaxResults: def, other: url.Values{}}
	_, q.ShowDeleted = args["show_deleted"]

	if v := args.Get("max_results"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && int(f) > 0 {
			q.MaxResults = int(f)
		}
	}
	if !p.Disabled && q.MaxResults > p.Limit {
		q.MaxResults = p.Limit
	}
	if v := args.Get("page"); v != "" && !p.Disabled {
		if n, err := strconv.Atoi(v); err == nil {
			q.Page = int(math.Abs(float64(n)))
			if q.Page == 0 {
				q.Page = 1
			}
		}
	}

	if v := args.Get("where"); v != "" {
		filter, err := parseWhere(v, denylist, idsAsString)
		if err != nil {
			return nil, err
		}
		q.Filter, q.rawWhere = filter, v
	}
	if v := args.Get("sort"); v != "" {
		sort, err := ParseSort(v)
		if err != nil {
			return nil, err
		}
		q.Sort, q.rawSort = sort, v
	}
	if v := args.Get("projection"); v != "" {
		p, err := parseProjection(v)
		if err != nil {
			return nil, err
		}
		q.Projection = p
	}

	for k, vs := range args {
		switch k {
		case "where", "sort", "page", "max_results", "embedded", "projection", "version":
			continue
		}
		q.other[k] = vs
	}
	return q, nil
}

// ParseWhere decodes a JSON Mongo query and converts values the way Eve's
// _mongotize does: strings in DATE_FORMAT become dates and 24 hex strings
// become ObjectIDs. Eve also accepts Python expressions (slug=="x"); that
// syntax is not supported and returns an error.
func ParseWhere(raw string, denylist []string) (bson.M, error) {
	return parseWhere(raw, denylist, false)
}

func parseWhere(raw string, denylist []string, idsAsString bool) (bson.M, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	var where map[string]any
	if err := dec.Decode(&where); err != nil {
		return nil, errBadRequest
	}
	if err := checkOperators(where, denylist); err != nil {
		return nil, err
	}
	return bson.M(mongotize(where, idsAsString).(map[string]any)), nil
}

func checkOperators(v any, denylist []string) error {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			for _, op := range denylist {
				if k == op {
					return ErrDeniedOperator{Operator: op}
				}
			}
			if err := checkOperators(val, denylist); err != nil {
				return err
			}
		}
	case []any:
		for _, val := range t {
			if err := checkOperators(val, denylist); err != nil {
				return err
			}
		}
	}
	return nil
}

var objectIDRe = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)

func mongotize(v any, idsAsString bool) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = mongotize(val, idsAsString)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = mongotize(val, idsAsString)
		}
		return t
	case string:
		if d, err := time.Parse(DateFormat, t); err == nil {
			return d
		}
		if !idsAsString && objectIDRe.MatchString(t) {
			if oid, err := bson.ObjectIDFromHex(t); err == nil {
				return oid
			}
		}
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		f, _ := t.Float64()
		return f
	}
	return v
}

func parseProjection(raw string) (bson.M, error) {
	var p map[string]any
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, errBadRequest
	}
	return bson.M(p), nil
}

var (
	emptySortRe = regexp.MustCompile(`^[\[{]\s*[\]}]$`)
	dictSortRe  = regexp.MustCompile(`^\{\s*["']([^"']+)["']\s*:\s*(-?1)\s*(?:,\s*["']([^"']+)["']\s*:\s*(-?1)\s*)*\}$`)
	dictPairRe  = regexp.MustCompile(`["']([^"']+)["']\s*:\s*(-?1)`)
)

var pythonSortRe = regexp.MustCompile(`\(\s*["']([^"']+)["']\s*(?:,\s*(-?1))?\s*\)|\[\s*["']([^"']+)["']\s*,\s*(-?1)\s*\]`)

// ParseSort accepts both of Eve's syntaxes: [("field", -1), ("other", 1)] and
// "-field,other". The first is read by ast.literal_eval, so pairs may also be
// lists, as in JSON ([["field", -1]]), and it may be a dict ({"field": -1}),
// sorted in the order of its keys. An empty list or dict is no sort.
func ParseSort(raw string) (bson.D, error) {
	raw = strings.TrimSpace(raw)
	var out bson.D
	if emptySortRe.MatchString(raw) {
		return nil, nil
	}
	if strings.HasPrefix(raw, "{") {
		if !dictSortRe.MatchString(raw) {
			return nil, errBadRequest
		}
		for _, m := range dictPairRe.FindAllStringSubmatch(raw, -1) {
			dir := 1
			if m[2] == "-1" {
				dir = -1
			}
			out = append(out, bson.E{Key: m[1], Value: dir})
		}
		return out, nil
	}
	if strings.HasPrefix(raw, "[") {
		matches := pythonSortRe.FindAllStringSubmatch(raw, -1)
		if len(matches) == 0 {
			return nil, errBadRequest
		}
		for _, m := range matches {
			field, sign := m[1], m[2]
			if field == "" {
				field, sign = m[3], m[4]
			}
			dir := 1
			if sign == "-1" {
				dir = -1
			}
			out = append(out, bson.E{Key: field, Value: dir})
		}
		return out, nil
	}
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		dir := 1
		if strings.HasPrefix(field, "-") {
			dir, field = -1, field[1:]
		}
		out = append(out, bson.E{Key: field, Value: dir})
	}
	if len(out) == 0 {
		return nil, errBadRequest
	}
	return out, nil
}

// queryString reproduces eve.utils.querydef: parameters are not URL encoded,
// max_results is omitted when it is the default and page when it is 1.
func (q *ListQuery) queryString(page, paginationDefault int) string {
	var b strings.Builder
	if q.MaxResults != paginationDefault {
		fmt.Fprintf(&b, "max_results=%d", q.MaxResults)
	}
	if q.rawWhere != "" {
		fmt.Fprintf(&b, "&where=%s", q.rawWhere)
	}
	if q.rawSort != "" {
		fmt.Fprintf(&b, "&sort=%s", q.rawSort)
	}
	if page > 1 {
		fmt.Fprintf(&b, "&page=%d", page)
	}
	keys := make([]string, 0, len(q.other))
	for k := range q.other {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range q.other[k] {
			fmt.Fprintf(&b, "&%s=%s", k, v)
		}
	}
	s := strings.TrimLeft(b.String(), "&")
	if s == "" {
		return ""
	}
	return "?" + s
}
