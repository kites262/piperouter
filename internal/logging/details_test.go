package logging

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDetailsCaptureDisabledIsCold(t *testing.T) {
	s := NewDetailsStore(1024, 1024, 1024)
	r, _ := http.NewRequest(http.MethodPost, "http://example.test/x", nil)
	if got := s.Begin(r); got != nil {
		t.Fatal("Begin returned a capture while disabled")
	}
}

func TestDetailsCaptureViewAndLongValue(t *testing.T) {
	s := NewDetailsStore(1<<20, 1<<20, 1<<20)
	s.SetEnabled(true)
	r, _ := http.NewRequest(http.MethodPost, "/chat?stream=true", strings.NewReader(`{"input":"0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789"}`))
	r.Header.Set("Authorization", "secret")
	c := s.Begin(r)
	if c == nil {
		t.Fatal("Begin returned nil while enabled")
	}
	r.Body = c.RequestBody(r.Body)
	if _, err := io.ReadAll(r.Body); err != nil {
		t.Fatal(err)
	}
	c.ResponseHeaders(http.Header{"Content-Type": {"application/json"}})
	c.ResponseBody([]byte(`{"answer":"0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij0123456789abcdefghij"}`))
	c.SetResult(200, "", "")
	c.Finish()

	v, ok := s.View(c.ID())
	if !ok {
		t.Fatal("captured details not found")
	}
	if v.RequestHeaders.Get("Authorization") != "[REDACTED]" {
		t.Fatalf("authorization was not redacted: %#v", v.RequestHeaders)
	}
	body, ok := v.RequestBody.(map[string]any)
	if !ok {
		t.Fatalf("request body type = %T", v.RequestBody)
	}
	preview, ok := body["input"].(map[string]any)
	if !ok || preview["type"] != "long_string" {
		t.Fatalf("long string preview = %#v", body["input"])
	}
	ref, _ := preview["value_ref"].(string)
	full, ok := s.Value(c.ID(), ref)
	if !ok || !strings.HasPrefix(full, "0123456789") || len(full) < 80 {
		t.Fatalf("full value unavailable: ok=%v len=%d", ok, len(full))
	}
}

func TestDetailsStoreEvictsByBytes(t *testing.T) {
	s := NewDetailsStore(1200, 1000, 100)
	s.SetEnabled(true)
	makeCapture := func(body string) string {
		r, _ := http.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		c := s.Begin(r)
		r.Body = c.RequestBody(r.Body)
		_, _ = io.ReadAll(r.Body)
		c.Finish()
		return c.ID()
	}
	first := makeCapture(strings.Repeat("a", 80))
	second := makeCapture(strings.Repeat("b", 80))
	if s.Has(first) {
		t.Fatal("oldest detail was not evicted")
	}
	if !s.Has(second) {
		t.Fatal("newest detail was evicted unexpectedly")
	}
}
