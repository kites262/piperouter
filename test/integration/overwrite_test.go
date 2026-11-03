package integration_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kites262/piperouter/internal/config"
)

func overwriteConfig(target string) *config.Config {
	r := route("llm", "/llm", target)
	r.Proxy.Overwrites = []string{"model-alias", "second-alias"}
	cfg := baseConfig(r)
	cfg.Overwrite = config.OverwriteConfig{
		Enabled: true,
		Rules: []config.OverwriteRule{
			{Name: "model-alias", Enabled: true, Field: "model", From: "gpt-6.1-sol", To: "gpt-6.1-sol-1"},
			{Name: "second-alias", Enabled: true, Field: "model", From: "gpt-6.1-sol-1", To: "another-model"},
		},
	}
	return cfg
}

func TestOverwriteTopLevelReplacement(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body) //nolint:errcheck // echo exactly what reached the upstream
	}))
	t.Cleanup(up.Close)
	ta := startApp(t, overwriteConfig(up.URL))
	client := newClient(t)

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "replace without chaining or changing other bytes",
			body: " {\n  \"model\" : \"gpt-6.1-sol\", \"large\":9007199254740993, \"nested\":{\"model\":\"gpt-6.1-sol\"}, \"messages\":[{\"content\":\"hello\"}]}\n",
			want: " {\n  \"model\" : \"gpt-6.1-sol-1\", \"large\":9007199254740993, \"nested\":{\"model\":\"gpt-6.1-sol\"}, \"messages\":[{\"content\":\"hello\"}]}\n",
		},
		{name: "second rule matches original value", body: `{"model":"gpt-6.1-sol-1"}`, want: `{"model":"another-model"}`},
		{name: "unmatched string", body: "{ \"model\" : \"other\", \"n\":1e+09 }", want: "{ \"model\" : \"other\", \"n\":1e+09 }"},
		{name: "missing top-level field", body: `{"nested":{"model":"gpt-6.1-sol"}}`, want: `{"nested":{"model":"gpt-6.1-sol"}}`},
		{name: "non-string field", body: `{"model":123}`, want: `{"model":123}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, got := postJSON(t, client, ta.proxyURL()+"/llm/chat/completions", tc.body)
			if resp.StatusCode != http.StatusOK || got != tc.want {
				t.Fatalf("status=%d body=%q, want 200 %q", resp.StatusCode, got, tc.want)
			}
		})
	}
	// No-body endpoints on the same route must not require a JSON Content-Type.
	if resp, _ := get(t, client, ta.proxyURL()+"/llm/models"); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET models status=%d, want 200", resp.StatusCode)
	}
}

func TestOverwriteIndependentConditionAndActionPaths(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body) //nolint:errcheck // compare the bytes the real upstream receives
	}))
	t.Cleanup(up.Close)
	r := route("llm", "/llm", up.URL)
	r.Proxy.Overwrites = []string{"routing-alias"}
	cfg := baseConfig(r)
	cfg.Overwrite = config.OverwriteConfig{
		Enabled: true,
		Rules: []config.OverwriteRule{{
			Name:      "routing-alias",
			Enabled:   true,
			Phase:     "req",
			Condition: config.OverwriteCondition{Path: ".routing.alias", Op: "eq", Value: "a"},
			Action:    config.OverwriteAction{Path: ".model", Op: "set", Value: "b"},
		}},
	}
	ta := startApp(t, cfg)
	client := newClient(t)
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "nested condition changes independent action path",
			body: " {\n \"routing\":{\"alias\":\"a\"},\"model\":\"original\",\"n\":9007199254740993}\n",
			want: " {\n \"routing\":{\"alias\":\"a\"},\"model\":\"b\",\"n\":9007199254740993}\n",
		},
		{
			name: "unmatched condition passes through",
			body: `{"routing":{"alias":"other"},"model":"original"}`,
			want: `{"routing":{"alias":"other"},"model":"original"}`,
		},
		{
			name: "missing condition passes through",
			body: `{"routing":{},"model":"original"}`,
			want: `{"routing":{},"model":"original"}`,
		},
		{
			name: "missing action target is not created",
			body: `{"routing":{"alias":"a"}}`,
			want: `{"routing":{"alias":"a"}}`,
		},
		{
			name: "non-string action target passes through",
			body: `{"routing":{"alias":"a"},"model":123}`,
			want: `{"routing":{"alias":"a"},"model":123}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, got := postJSON(t, client, ta.proxyURL()+"/llm/chat/completions", tc.body)
			if resp.StatusCode != http.StatusOK || got != tc.want {
				t.Fatalf("status=%d body=%q, want 200 %q", resp.StatusCode, got, tc.want)
			}
		})
	}
}

func TestOverwriteDisabledPathsRemainTransparent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*config.Config)
	}{
		{name: "global disabled", modify: func(c *config.Config) { c.Overwrite.Enabled = false }},
		{name: "rules disabled", modify: func(c *config.Config) {
			for i := range c.Overwrite.Rules {
				c.Overwrite.Rules[i].Enabled = false
			}
		}},
		{name: "route unbound", modify: func(c *config.Config) { c.Routes[0].Proxy.Overwrites = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(w, r.Body) //nolint:errcheck
			}))
			t.Cleanup(up.Close)
			cfg := overwriteConfig(up.URL)
			tc.modify(cfg)
			ta := startApp(t, cfg)
			client := newClient(t)
			// Non-JSON is intentionally accepted when no active binding exists.
			const body = "opaque upload: model=gpt-6.1-sol\x00"
			resp, err := client.Post(ta.proxyURL()+"/llm/upload", "application/octet-stream", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || string(got) != body {
				t.Fatalf("status=%d body=%q err=%v, want transparent upload", resp.StatusCode, got, err)
			}
		})
	}
}

// An upstream-held response proves the first SSE frame is visible before the
// response completes. The same open stream then survives a real YAML hot reload.
func TestOverwriteSSEAndHotReload(t *testing.T) {
	release := make(chan struct{}, 1)
	defer close(release)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/stream" {
			fmt.Fprint(w, body.Model)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", body.Model)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprintf(w, "data: still-%s\n\n", body.Model)
	}))
	t.Cleanup(up.Close)
	ta := startApp(t, overwriteConfig(up.URL))
	client := newClient(t)
	// This deadline bounds any regression that buffers an unfinished SSE response.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ta.proxyURL()+"/llm/stream", strings.NewReader(`{"model":"gpt-6.1-sol","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("first SSE headers were not delivered while upstream held the response: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status=%d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	if err != nil || first != "data: gpt-6.1-sol-1\n" {
		t.Fatalf("first SSE frame=%q err=%v", first, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}

	revision := adminStatus(t, client, ta).Config.Revision
	cfg, err := config.Load(ta.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Overwrite.Rules[0].Action.Value = "updated-model"
	writeConfig(t, ta.ConfigPath, cfg)
	eventually(t, 5*time.Second, "Overwrite configuration did not hot-reload", func() bool {
		status := adminStatus(t, client, ta)
		return status.Config.Valid && status.Config.Revision != revision
	})
	updated, got := postJSON(t, client, ta.proxyURL()+"/llm/chat/completions", `{"model":"gpt-6.1-sol"}`)
	if updated.StatusCode != http.StatusOK || got != "updated-model" {
		t.Fatalf("new request status=%d body=%q, want updated-model", updated.StatusCode, got)
	}

	// Release exactly one waiting handler without closing the channel twice.
	release <- struct{}{}
	last, err := reader.ReadString('\n')
	if err != nil || last != "data: still-gpt-6.1-sol-1\n" {
		t.Fatalf("existing stream after reload=%q err=%v", last, err)
	}
}
