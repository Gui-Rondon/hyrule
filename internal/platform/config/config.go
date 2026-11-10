// Package config loads the service configuration from properties-api, the
// Spring Cloud Config Server that serves the config-files repository.
//
// It mirrors the Python config-client used by the Eve APIs: it reads
// CONFIGSERVER_ADDRESS, APP_NAME, PROFILE and LABEL, requests
// {address}/{app}/{profile}/{label} and merges the property sources from the
// lowest to the highest precedence, expanding flat keys such as
// "mongo.uri" and "azure.audiences[0]" into nested maps and lists.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Options selects which configuration to load.
type Options struct {
	Address string
	App     string
	Profile string
	Label   string
	Timeout time.Duration
	Client  *http.Client
}

// OptionsFromEnv builds Options with the same variables and defaults as the
// Python config-client.
func OptionsFromEnv(app string) Options {
	timeout := 3 * time.Second
	if v, err := strconv.Atoi(os.Getenv("CONFIG_TIMEOUT")); err == nil {
		timeout = time.Duration(v) * time.Second
	}
	return Options{
		Address: getenv("CONFIGSERVER_ADDRESS", "http://localhost:8888"),
		App:     getenv("APP_NAME", app),
		Profile: getenv("PROFILE", "development"),
		Label:   getenv("LABEL", "master"),
		Timeout: timeout,
	}
}

// Config is the merged configuration tree.
type Config map[string]any

type springResponse struct {
	PropertySources []struct {
		Name   string         `json:"name"`
		Source map[string]any `json:"source"`
	} `json:"propertySources"`
}

// Load requests the configuration from the config server.
func Load(ctx context.Context, opts Options) (Config, error) {
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}
	url := fmt.Sprintf("%s/%s/%s/%s", strings.TrimRight(opts.Address, "/"), opts.App, opts.Profile, opts.Label)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("config: request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("config: request %s: status %d", url, resp.StatusCode)
	}
	var body springResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", url, err)
	}
	cfg := Config{}
	// the first property source has the highest precedence
	for i := len(body.PropertySources) - 1; i >= 0; i-- {
		mergeInto(cfg, expand(body.PropertySources[i].Source))
	}
	if len(cfg) == 0 {
		return nil, fmt.Errorf("config: empty configuration from %s", url)
	}
	return cfg, nil
}

// LoadFile reads a JSON file with the already nested configuration. It is
// meant for local development without access to properties-api.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Config{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: decode %s: %w", path, err)
	}
	return cfg, nil
}

// LoadFromEnv loads from CONFIG_FILE when set, otherwise from properties-api.
// CONFIG_OVERRIDES, a JSON file in the CONFIG_FILE format, is merged on top:
// it lets a local run use the staging config with, e.g., a local Redis.
func LoadFromEnv(ctx context.Context, app string) (Config, error) {
	var cfg Config
	var err error
	if path := os.Getenv("CONFIG_FILE"); path != "" {
		cfg, err = LoadFile(path)
	} else {
		cfg, err = Load(ctx, OptionsFromEnv(app))
	}
	if err != nil {
		return nil, err
	}
	if path := os.Getenv("CONFIG_OVERRIDES"); path != "" {
		overrides, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		mergeInto(cfg, overrides)
	}
	return cfg, nil
}

// Get returns the value at a dotted path, e.g. "mongo.uri".
func (c Config) Get(path string) (any, bool) {
	var cur any = map[string]any(c)
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// String returns the value at path as a string, or def when missing.
func (c Config) String(path, def string) string {
	v, ok := c.Get(path)
	if !ok || v == nil {
		return def
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Bool returns the value at path as a bool, accepting the same strings as
// Python's strtobool ("t", "true", "1", "yes", "on"...).
func (c Config) Bool(path string, def bool) bool {
	v, ok := c.Get(path)
	if !ok || v == nil {
		return def
	}
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		switch strings.ToLower(b) {
		case "y", "yes", "t", "true", "on", "1":
			return true
		case "n", "no", "f", "false", "off", "0":
			return false
		}
	}
	return def
}

// Strings returns the value at path as a list of strings.
func (c Config) Strings(path string) []string {
	v, ok := c.Get(path)
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		if s, ok := v.(string); ok {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

// Sub returns the subtree at path, or an empty Config.
func (c Config) Sub(path string) Config {
	v, ok := c.Get(path)
	if !ok {
		return Config{}
	}
	if m, ok := v.(map[string]any); ok {
		return Config(m)
	}
	return Config{}
}

var indexRe = regexp.MustCompile(`^(.*)\[(\d+)\]$`)

// expand turns {"a.b[0].c": 1} into {"a": {"b": [{"c": 1}]}}.
func expand(flat map[string]any) map[string]any {
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, key := range keys {
		setPath(out, strings.Split(key, "."), flat[key])
	}
	return listify(out).(map[string]any)
}

// setPath stores lists as maps keyed by "[n]" so they can be filled in any
// order; listify converts them back afterwards.
func setPath(node map[string]any, parts []string, value any) {
	part := parts[0]
	var segments []string
	for {
		m := indexRe.FindStringSubmatch(part)
		if m == nil {
			break
		}
		segments = append([]string{"[" + m[2] + "]"}, segments...)
		part = m[1]
	}
	segments = append([]string{part}, segments...)

	for i, seg := range segments {
		last := i == len(segments)-1 && len(parts) == 1
		if last {
			node[seg] = value
			return
		}
		child, ok := node[seg].(map[string]any)
		if !ok {
			child = map[string]any{}
			node[seg] = child
		}
		node = child
	}
	setPath(node, parts[1:], value)
}

func listify(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	isList := len(m) > 0
	for k := range m {
		if !strings.HasPrefix(k, "[") {
			isList = false
		}
		m[k] = listify(m[k])
	}
	if !isList {
		return m
	}
	idx := make([]int, 0, len(m))
	for k := range m {
		n, _ := strconv.Atoi(strings.Trim(k, "[]"))
		idx = append(idx, n)
	}
	sort.Ints(idx)
	list := make([]any, 0, len(idx))
	for _, n := range idx {
		list = append(list, m["["+strconv.Itoa(n)+"]"])
	}
	return list
}

// mergeInto deep merges src into dst; lists and scalars from src replace dst.
func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeInto(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
