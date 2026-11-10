package evecompat

import (
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseListQueryPagination(t *testing.T) {
	cases := []struct {
		query          string
		page, maxItems int
	}{
		{"", 1, 25},
		{"max_results=10", 1, 10},
		{"max_results=100", 1, 50},
		{"max_results=abc", 1, 25},
		{"max_results=0", 1, 25},
		{"max_results=-5", 1, 25},
		{"max_results=2.9", 1, 2},
		{"page=3", 3, 25},
		{"page=-2", 2, 25},
		{"page=0", 1, 25},
		{"page=abc", 1, 25},
	}
	for _, tc := range cases {
		args, _ := url.ParseQuery(tc.query)
		q, err := ParseListQuery(args, Pagination{Default: 25, Limit: 50}, DefaultQueryDenylist)
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if q.Page != tc.page || q.MaxResults != tc.maxItems {
			t.Errorf("%q: page=%d max_results=%d, want %d %d", tc.query, q.Page, q.MaxResults, tc.page, tc.maxItems)
		}
	}
}

func TestParseWhereConvertsLikeEve(t *testing.T) {
	filter, err := ParseWhere(`{"_id":"6ada449701ecde6e488b8ac9","_created":{"$gte":"Thu, 01 Jan 2026 00:00:00 GMT"},"n":{"$in":[1,2.5]},"slug":"gremio"}`, DefaultQueryDenylist)
	if err != nil {
		t.Fatal(err)
	}
	oid, _ := bson.ObjectIDFromHex("6ada449701ecde6e488b8ac9")
	if filter["_id"] != oid {
		t.Errorf("_id = %#v", filter["_id"])
	}
	created := filter["_created"].(map[string]any)["$gte"]
	if created != time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("_created = %#v", created)
	}
	if in := filter["n"].(map[string]any)["$in"]; !reflect.DeepEqual(in, []any{int64(1), 2.5}) {
		t.Errorf("$in = %#v", in)
	}
	if filter["slug"] != "gremio" {
		t.Errorf("slug = %#v", filter["slug"])
	}
}

func TestParseWhereRejects(t *testing.T) {
	var denied ErrDeniedOperator
	if _, err := ParseWhere(`{"$or":[{"$where":"1==1"}]}`, DefaultQueryDenylist); !errors.As(err, &denied) {
		t.Errorf("nested $where must be denied, got %v", err)
	}
	if _, err := ParseWhere(`{"slug":{"$regex":"^a"}}`, append([]string{"$regex"}, DefaultQueryDenylist...)); !errors.As(err, &denied) {
		t.Errorf("$regex must be denied when listed, got %v", err)
	}
	if _, err := ParseWhere(`slug=="inter"`, DefaultQueryDenylist); err == nil {
		t.Error("python expressions are not supported")
	}
}

func TestParseSort(t *testing.T) {
	want := bson.D{{Key: "slug", Value: 1}, {Key: "public", Value: -1}}
	// ast.literal_eval in Eve: tuples or lists (the JSON form)
	for _, raw := range []string{`[("slug", 1), ("public", -1)]`, `[('slug',1),('public',-1)]`, `[["slug", 1], ["public", -1]]`,
		`{"slug": 1, "public": -1}`, `{'slug':1,'public':-1}`, `slug,-public`} {
		got, err := ParseSort(raw)
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %v", raw, got)
		}
	}
	// as the Eve 2.3.1 reference answers: 400, and no sort for an empty one
	for _, raw := range []string{`[("slug", 1]]`, `[1]`, `{"slug"}`} {
		if got, err := ParseSort(raw); err == nil {
			t.Errorf("%q: %v, want an error", raw, got)
		}
	}
	for _, raw := range []string{`[]`, `{}`} {
		if got, err := ParseSort(raw); err != nil || len(got) != 0 {
			t.Errorf("%q: %v %v, want no sort", raw, got, err)
		}
	}
}

func TestQueryStringMatchesEveQuerydef(t *testing.T) {
	args, _ := url.ParseQuery(`max_results=1&where={"public":true}&sort=-slug&projection={"slug":1}&foo=bar`)
	q, err := ParseListQuery(args, Pagination{Default: 25, Limit: 50}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `?max_results=1&where={"public":true}&sort=-slug&page=2&foo=bar`
	if got := q.queryString(2, 25); got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if got := q.queryString(1, 25); got != `?max_results=1&where={"public":true}&sort=-slug&foo=bar` {
		t.Errorf("page 1 must be omitted: %s", got)
	}
}

func TestParseListQueryWithPaginationDisabled(t *testing.T) {
	p := Pagination{Default: 25, Limit: 50, Disabled: true}
	for query, want := range map[string]int{"": 0, "max_results=100": 100, "max_results=abc": 0, "page=3": 0} {
		args, _ := url.ParseQuery(query)
		q, err := ParseListQuery(args, p, nil)
		if err != nil {
			t.Fatal(err)
		}
		if q.MaxResults != want || q.Page != 1 {
			t.Errorf("%q: max_results=%d page=%d", query, q.MaxResults, q.Page)
		}
	}
}
