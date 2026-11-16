package postgres

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestCompileWhereBindingsAndErrorsMatchFilter(t *testing.T) {
	field := "quo'te\\雪; DROP TABLE comments; --"
	value := "x' OR true; --\\🙂"
	schema := SchemaSpec{Fields: map[string]Kind{"n": KindInteger, "when": KindDateTime, "tags": KindArray}, ElementKinds: map[string]Kind{"tags": KindString}}
	valid := []Query{
		nil, {}, {field: value}, {"meta." + field: value},
		{"$not": Query{field: value}},
		{"$and": []Query{{"n": Query{"$gt": 10, "$lt": 100}}, {"when": Query{"$gte": "2020-01-01T00:00:00Z"}}}},
		{"$or": []Query{{"n": Query{"$eq": nil}}, {"$not": Query{"n": Query{"$exists": true}}}}},
		{"tags": Query{"$has": value}},
	}
	placeholders := regexp.MustCompile(`\$([0-9]+)`)
	for _, q := range valid {
		for _, offset := range []int{0, 7} {
			_, oldArgs, err := CompileFilter(q, schema, offset)
			if err != nil {
				t.Fatal(err)
			}
			sql, args, err := CompileWhere(q, schema, offset)
			if err != nil || !reflect.DeepEqual(args, oldArgs) {
				t.Fatalf("query=%v offset=%d args=%v want=%v err=%v", q, offset, args, oldArgs, err)
			}
			if strings.Contains(sql, field) || strings.Contains(sql, value) {
				t.Fatalf("input interpolated: %s", sql)
			}
			seen := map[int]bool{}
			for _, match := range placeholders.FindAllStringSubmatch(sql, -1) {
				n, _ := strconv.Atoi(match[1])
				if n <= offset || n > offset+len(args) {
					t.Fatalf("invalid offset/binding $%d: %s args=%v", n, sql, args)
				}
				seen[n] = true
			}
			if len(seen) != len(args) {
				t.Fatalf("orphan bindings: %s args=%v", sql, args)
			}
		}
	}
	invalid := []Query{
		{"$not": "wrong"}, {"$or": []Query{}}, {"n": Query{"$gt": nil}},
		{"n": Query{"$gt": "bad"}}, {"when": "not-a-date"}, {"tags": Query{"$unknown": true}},
		{"meta.n": Query{"$eq": 1, "$exists": true}},
	}
	for _, q := range invalid {
		_, _, oldErr := CompileFilter(q, schema, 0)
		_, _, err := CompileWhere(q, schema, 0)
		if oldErr == nil || err == nil || oldErr.Error() != err.Error() {
			t.Fatalf("error changed: query=%v filter=%v where=%v", q, oldErr, err)
		}
	}
}
