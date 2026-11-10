package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// The cases of docs/ci.md against the real import graph of the repository
// (the examples/blog-apis reference adopter).
func TestRealGraph(t *testing.T) {
	const dir = "examples/blog-apis/services"
	const deployDir = "examples/blog-apis/deploy"
	services, err := builds(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := Select(services, Change{Toolchain: true}, deployDir)
	if len(all) != 4 {
		t.Fatalf("services = %v", all)
	}
	cases := []struct {
		name  string
		files []string
		want  []string
	}{
		{"evecompat", []string{"internal/evecompat/http.go"}, all},
		{"outbox", []string{"internal/outbox/relay.go"}, []string{"posts-api"}},
		{"redis", []string{"internal/platform/redis/redis.go"}, []string{"posts-api"}},
		{"searchsync", []string{"internal/behaviors/searchsync/searchsync.go"}, []string{"authors-api", "posts-api", "tags-api"}},
		{"audit", []string{"internal/behaviors/audit/audit.go"}, []string{"authors-api", "posts-api"}},
		{"one service", []string{dir + "/categories-api/categories.go"}, []string{"categories-api"}},
		{"deleted file", []string{"internal/outbox/removed.go"}, []string{"posts-api"}},
		{"deleted service file", []string{dir + "/posts-api/removed.go"}, []string{"posts-api"}},
		{"deleted test file", []string{dir + "/posts-api/removed_test.go"}, nil},
		{"manifest", []string{deployDir + "/posts-api/manifest.yml"}, []string{"posts-api"}},
		{"test file", []string{"internal/evecompat/http_test.go", dir + "/tags-api/tags_test.go"}, nil},
		{"testkit", []string{"internal/testkit/testkit.go"}, nil},
		{"docs", []string{"examples/blog-apis/README.md", "README.md"}, nil},
		{"Dockerfile", []string{"Dockerfile"}, all},
		{"ci", []string{"ci/affected/main.go"}, all},
	}
	for _, c := range cases {
		got := Select(services, Change{Files: c.files}, deployDir)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
	// a module used only by posts-api (the Redis-backed schedule endpoint)
	if got := Select(services, Change{Modules: []string{"github.com/redis/go-redis/v9"}}, deployDir); !slices.Equal(got, []string{"posts-api"}) {
		t.Errorf("redis module = %v", got)
	}
}

func TestParseModDiff(t *testing.T) {
	diff := []byte(`diff --git a/go.mod b/go.mod
--- a/go.mod
+++ b/go.mod
@@ -5 +5 @@
-	github.com/go-ldap/ldap/v3 v3.4.15
+	github.com/go-ldap/ldap/v3 v3.4.16
diff --git a/go.sum b/go.sum
@@ -1,2 +1,2 @@
-github.com/go-ldap/ldap/v3 v3.4.15 h1:abc=
+github.com/go-ldap/ldap/v3 v3.4.16 h1:def=
+golang.org/x/text v0.44.0/go.mod h1:x=
`)
	mods, tc := parseModDiff(diff)
	if tc || !slices.Equal(mods, []string{"github.com/go-ldap/ldap/v3", "golang.org/x/text"}) {
		t.Errorf("modules = %v toolchain = %v", mods, tc)
	}
	if _, tc := parseModDiff([]byte("-go 1.27\n+go 1.28\n")); !tc {
		t.Error("go directive change not detected")
	}
}

func TestRun(t *testing.T) {
	var out bytes.Buffer
	if err := run("examples/blog-apis/services/tags-api/tags.go", "", defaultServicesDir, &out); err != nil || strings.TrimSpace(out.String()) != "tags-api" {
		t.Errorf("run = %q %v", out.String(), err)
	}
	if err := run("", "", defaultServicesDir, &out); err == nil {
		t.Error("no arguments accepted")
	}
}
