// Package search writes to Elasticsearch through its REST API
// (PUT/POST/DELETE /{index}/_doc|_update/{id}), which is what the Python
// client 7.x used by the Eve APIs and workers does, so no client library is
// needed.
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Gui-Rondon/hyrule/internal/platform/config"
)

// Client talks to a cluster, trying every host in turn.
type Client struct {
	hosts []string
	http  *http.Client
	next  atomic.Uint64
}

// New builds a client for the given base URLs.
func New(hosts []string) *Client {
	return &Client{hosts: hosts, http: &http.Client{Timeout: 5 * time.Second}}
}

// FromConfig reads elasticsearch.hosts from config.Config:
//
//	elasticsearch:
//	  hosts:
//	    - host: search.example.com
//	      port: 9200
func FromConfig(cfg config.Config) (*Client, error) {
	raw, _ := cfg.Get("elasticsearch.hosts")
	hosts, err := parseHosts(raw)
	if err != nil {
		return nil, err
	}
	return New(hosts), nil
}

func parseHosts(raw any) ([]string, error) {
	var items []any
	switch t := raw.(type) {
	case []any:
		items = t
	case string, map[string]any:
		items = []any{t}
	}
	var hosts []string
	for _, item := range items {
		switch h := item.(type) {
		case string:
			if !strings.Contains(h, "://") {
				h = "http://" + h
			}
			hosts = append(hosts, strings.TrimRight(h, "/"))
		case map[string]any:
			host, _ := h["host"].(string)
			port := "9200"
			if h["port"] != nil {
				port = fmt.Sprint(h["port"])
			}
			scheme := "http"
			if s, ok := h["scheme"].(string); ok && s != "" {
				scheme = s
			}
			hosts = append(hosts, fmt.Sprintf("%s://%s:%s", scheme, host, port))
		}
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("search: elasticsearch.hosts is required")
	}
	return hosts, nil
}

// WriteOption changes a write.
type WriteOption func(*url.Values)

// Refresh makes the write visible to searches right away, as Eve-Elastic
// does after every insert (refresh=true).
func Refresh() WriteOption {
	return func(q *url.Values) { q.Set("refresh", "true") }
}

func query(opts []WriteOption) string {
	q := url.Values{}
	for _, opt := range opts {
		opt(&q)
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// Index stores doc under id; doc must not contain _id.
func (c *Client) Index(ctx context.Context, index, id string, doc any, opts ...WriteOption) error {
	return c.write(ctx, http.MethodPut, "/"+index+"/_doc/"+url.PathEscape(id)+query(opts), doc, false)
}

// Update merges partial into the stored document.
func (c *Client) Update(ctx context.Context, index, id string, partial any) error {
	return c.write(ctx, http.MethodPost, "/"+index+"/_update/"+url.PathEscape(id), map[string]any{"doc": partial}, false)
}

// Delete removes the document; a missing document is not an error.
func (c *Client) Delete(ctx context.Context, index, id string, opts ...WriteOption) error {
	return c.write(ctx, http.MethodDelete, "/"+index+"/_doc/"+url.PathEscape(id)+query(opts), nil, true)
}

// Check is the healthcheck checker ("Elasticsearch is Up!").
func (c *Client) Check(ctx context.Context) (bool, string) {
	resp, err := c.do(ctx, http.MethodHead, "/", nil)
	if err != nil || resp.StatusCode >= 300 {
		return false, "Elasticsearch is Down :("
	}
	return true, "Elasticsearch is Up!"
}

func (c *Client) write(ctx context.Context, method, path string, doc any, notFoundOK bool) error {
	var body []byte
	if doc != nil {
		var err error
		if body, err = json.Marshal(doc); err != nil {
			return err
		}
	}
	resp, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound && notFoundOK {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("search: %s %s: status %d", method, path, resp.StatusCode)
	}
	return nil
}

// do tries every host, starting from a different one each call.
func (c *Client) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	start := int(c.next.Add(1))
	var lastErr error
	for i := range c.hosts {
		host := c.hosts[(start+i)%len(c.hosts)]
		req, err := http.NewRequestWithContext(ctx, method, host+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp, nil
	}
	return nil, fmt.Errorf("search: all hosts failed: %w", lastErr)
}
