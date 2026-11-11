package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestLoadMergesPropertySourcesByPrecedence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/tags-api/production/master" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`{"propertySources": [
			{"name": "tags-api-production.yml", "source": {
				"mongo.uri": "mongodb://prd/tags",
				"elasticsearch.index": "tags"}},
			{"name": "application-elasticsearch-production.yml", "source": {
				"elasticsearch.index": "default",
				"elasticsearch.hosts[0].host": "es01", "elasticsearch.hosts[0].port": 9200,
				"elasticsearch.hosts[1].host": "es02", "elasticsearch.hosts[1].port": 9200}},
			{"name": "application-azure.yml", "source": {
				"azure.issuer": "https://login/v2.0",
				"azure.audiences[1]": "prd", "azure.audiences[0]": "dev"}}
		]}`))
	}))
	defer srv.Close()

	cfg, err := Load(context.Background(), Options{Address: srv.URL, App: "tags-api", Profile: "production", Label: "master"})
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.String("mongo.uri", ""); got != "mongodb://prd/tags" {
		t.Errorf("mongo.uri = %q", got)
	}
	if got := cfg.String("elasticsearch.index", ""); got != "tags" {
		t.Errorf("higher precedence source must win, elasticsearch.index = %q", got)
	}
	if got := cfg.Strings("azure.audiences"); !reflect.DeepEqual(got, []string{"dev", "prd"}) {
		t.Errorf("azure.audiences = %v", got)
	}
	hosts, _ := cfg.Get("elasticsearch.hosts")
	want := []any{
		map[string]any{"host": "es01", "port": float64(9200)},
		map[string]any{"host": "es02", "port": float64(9200)},
	}
	if !reflect.DeepEqual(hosts, want) {
		t.Errorf("elasticsearch.hosts = %#v", hosts)
	}
}

func TestLoadFailsOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	if _, err := Load(context.Background(), Options{Address: srv.URL, App: "x", Profile: "y", Label: "z"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestBoolAcceptsStrtoboolValues(t *testing.T) {
	cfg := Config{"auth": map[string]any{"enable": "t", "off": "false"}}
	if !cfg.Bool("auth.enable", false) {
		t.Error("auth.enable should be true")
	}
	if cfg.Bool("auth.off", true) {
		t.Error("auth.off should be false")
	}
	if !cfg.Bool("auth.missing", true) {
		t.Error("missing key should return default")
	}
}

func TestLoadFileAndEnv(t *testing.T) {
	path := t.TempDir() + "/cfg.json"
	if err := os.WriteFile(path, []byte(`{"mongo": {"uri": "mongodb://x"}, "list": ["a", 1], "n": 3, "s": "v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := LoadFromEnv(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sub("mongo").String("uri", "") != "mongodb://x" || cfg.String("n", "") != "3" {
		t.Errorf("cfg = %v", cfg)
	}
	if got := cfg.Strings("list"); !reflect.DeepEqual(got, []string{"a", "1"}) {
		t.Errorf("Strings = %v", got)
	}
	if got := cfg.Strings("s"); !reflect.DeepEqual(got, []string{"v"}) {
		t.Errorf("Strings of a string = %v", got)
	}
	if len(cfg.Sub("nada")) != 0 || cfg.Strings("nada") != nil || cfg.String("mongo.uri.x", "d") != "d" {
		t.Error("missing paths")
	}
	if !cfg.Bool("n", false) {
		t.Error("number as bool")
	}

	os.WriteFile(path, []byte(`{bad`), 0o600)
	if _, err := LoadFile(path); err == nil {
		t.Error("invalid json must fail")
	}
}

func TestLoadFromEnvMergesOverrides(t *testing.T) {
	dir := t.TempDir()
	base := dir + "/base.json"
	over := dir + "/over.json"
	os.WriteFile(base, []byte(`{"redis": {"host": "redis-hlg", "port": 6379}, "auth": {"enable": true}, "list": ["a", "b"]}`), 0o600)
	os.WriteFile(over, []byte(`{"redis": {"host": "localhost"}, "list": ["c"]}`), 0o600)
	t.Setenv("CONFIG_FILE", base)
	t.Setenv("CONFIG_OVERRIDES", over)
	cfg, err := LoadFromEnv(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.String("redis.host", "") != "localhost" || cfg.String("redis.port", "") != "6379" {
		t.Errorf("redis = %v", cfg.Sub("redis"))
	}
	if !cfg.Bool("auth.enable", false) {
		t.Error("keys outside the overrides must stay")
	}
	if got := cfg.Strings("list"); !reflect.DeepEqual(got, []string{"c"}) {
		t.Errorf("a list is replaced, not merged: %v", got)
	}

	os.WriteFile(over, []byte(`{bad`), 0o600)
	if _, err := LoadFromEnv(context.Background(), "x"); err == nil {
		t.Error("invalid overrides must fail")
	}
	t.Setenv("CONFIG_OVERRIDES", dir+"/missing.json")
	if _, err := LoadFromEnv(context.Background(), "x"); err == nil {
		t.Error("missing overrides file must fail")
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("CONFIGSERVER_ADDRESS", "http://cfg")
	t.Setenv("PROFILE", "production")
	t.Setenv("LABEL", "")
	t.Setenv("CONFIG_TIMEOUT", "7")
	o := OptionsFromEnv("tags-api")
	if o.Address != "http://cfg" || o.App != "tags-api" || o.Profile != "production" || o.Label != "master" || o.Timeout.Seconds() != 7 {
		t.Errorf("options = %+v", o)
	}
}
