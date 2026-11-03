package diagnostics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/kites262/piperouter/internal/config"
	"github.com/kites262/piperouter/internal/diagnostics"
	"github.com/kites262/piperouter/internal/overwrite"
)

func TestDiagnosticsJSONBodyUsesActiveRules(t *testing.T) {
	const original = ` {"routing":{"alias":"fast"},"model":"a","n":9007199254740993} `
	const changed = ` {"routing":{"alias":"fast"},"model":"b","n":9007199254740993} `
	var received string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = string(body)
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer test" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Digest") != "" {
			t.Errorf("unexpected outgoing probe: %s %v", r.Method, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer up.Close()
	cfg := &config.Config{Routes: []config.RouteConfig{route("api", "/api", up.URL, true)}, Overwrite: config.OverwriteConfig{Enabled: true, Rules: []config.OverwriteRule{
		{Name: "alias", Enabled: true, Condition: config.OverwriteCondition{Path: ".routing.alias", Op: "eq", Value: "fast"}, Action: config.OverwriteAction{Path: ".model", Op: "set", Value: "b"}},
	}}}
	cfg.Routes[0].Proxy.Overwrites = []string{"alias"}
	snap := buildSnapshot(t, cfg)
	snap.Table.Routes()[0].Overwrite = overwrite.Compile(cfg.Overwrite, []string{"alias"})
	res := diagnostics.TestRequest(context.Background(), snap, diagnostics.RequestTest{
		Path: "/api/chat", Method: "POST", Body: original,
		Headers: map[string]string{"Authorization": "Bearer test", "X-Forwarded-For": "192.0.2.1", "Digest": "old"},
	})
	if !res.OK || res.Status != http.StatusNoContent || !res.BodyChanged || res.RequestBody == nil || *res.RequestBody != changed || received != changed {
		t.Fatalf("unexpected diagnostic result: %+v, sent=%q", res, received)
	}
}

func TestDiagnosticsInvalidBodyOrHeadersDoNotContactUpstream(t *testing.T) {
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	cfg := &config.Config{Routes: []config.RouteConfig{route("api", "/api", up.URL, true)}, Overwrite: config.OverwriteConfig{Enabled: true, Rules: []config.OverwriteRule{
		{Name: "alias", Enabled: true, Field: "model", From: "a", To: "b"},
	}}}
	cfg.Routes[0].Proxy.Overwrites = []string{"alias"}
	snap := buildSnapshot(t, cfg)
	snap.Table.Routes()[0].Overwrite = overwrite.Compile(cfg.Overwrite, []string{"alias"})
	for _, request := range []diagnostics.RequestTest{
		{Path: "/api", Method: "POST", Body: `{"model":`},
		{Path: "/api", Method: "GET", Body: `{"model":"a"}`},
		{Path: "/api", Method: "POST", Headers: map[string]string{"Bad Header": "x"}},
		{Path: "/api", Method: "POST", Headers: map[string]string{"Authorization": "x\r\ny"}},
		{Path: "/api", Method: "POST", Headers: map[string]string{"Connection": "upgrade"}},
		{Path: "/api", Method: "POST", Body: `{}`, Headers: map[string]string{"Content-Encoding": "gzip"}},
	} {
		res := diagnostics.TestRequest(context.Background(), snap, request)
		if res.OK || res.ErrorStage != diagnostics.StageResolve {
			t.Errorf("accepted invalid probe: %+v", res)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid probes contacted upstream")
	}
}
