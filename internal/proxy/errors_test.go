package proxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// timeoutError mimics net/http's private timeout errors (e.g. "net/http:
// timeout awaiting response headers").
type timeoutError struct{ msg string }

func (e timeoutError) Error() string   { return e.msg }
func (e timeoutError) Timeout() bool   { return true }
func (e timeoutError) Temporary() bool { return false }

func TestClassifyUpstreamError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantClass  string
	}{
		{
			name: "dial refused",
			err: &url.Error{Op: "Get", URL: "http://u.test", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name: "dial timeout is a connect failure, not 504",
			err: &url.Error{Op: "Get", URL: "http://u.test", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: timeoutError{msg: "i/o timeout"}}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name: "dns not found",
			err: &url.Error{Op: "Get", URL: "http://u.test", Err: &net.OpError{
				Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "u.test", IsNotFound: true}}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "tls record header",
			err:        &url.Error{Op: "Get", URL: "https://u.test", Err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "x509 unknown authority",
			err:        &url.Error{Op: "Get", URL: "https://u.test", Err: x509.UnknownAuthorityError{}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "tls handshake timeout is a connect failure",
			err:        &url.Error{Op: "Get", URL: "https://u.test", Err: timeoutError{msg: "net/http: TLS handshake timeout"}},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "http CONNECT proxy failure",
			err:        &url.Error{Op: "Get", URL: "http://u.test", Err: errors.New("proxy CONNECT returned 403 Forbidden")},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "socks5 negotiation failure",
			err:        &url.Error{Op: "Get", URL: "http://u.test", Err: errors.New("socks connect tcp: unknown error general SOCKS server failure")},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "response header timeout",
			err:        &url.Error{Op: "Get", URL: "http://u.test", Err: timeoutError{msg: "net/http: timeout awaiting response headers"}},
			wantStatus: 504, wantClass: "upstream_timeout",
		},
		{
			name:       "os deadline exceeded",
			err:        fmt.Errorf("read: %w", os.ErrDeadlineExceeded),
			wantStatus: 504, wantClass: "upstream_timeout",
		},
		{
			name:       "upstream hangup",
			err:        &url.Error{Op: "Get", URL: "http://u.test", Err: errors.New("EOF")},
			wantStatus: 502, wantClass: "upstream_connection_failed",
		},
		{
			name:       "context canceled",
			err:        &url.Error{Op: "Get", URL: "http://u.test", Err: context.Canceled},
			wantStatus: 0, wantClass: "client_canceled",
		},
		{
			name:       "wrapped context canceled",
			err:        fmt.Errorf("while proxying: %w", context.Canceled),
			wantStatus: 0, wantClass: "client_canceled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, class := classifyUpstreamError(tc.err)
			if status != tc.wantStatus || class != tc.wantClass {
				t.Fatalf("classifyUpstreamError(%v) = (%d, %q), want (%d, %q)",
					tc.err, status, class, tc.wantStatus, tc.wantClass)
			}
		})
	}
}

func TestWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, 404, "route_not_found")
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if got, want := rec.Body.String(), "{\"error\":\"route_not_found\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestIsEventStream(t *testing.T) {
	cases := []struct {
		ct   string
		want bool
	}{
		{"text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		{"TEXT/EVENT-STREAM", true},
		{"  text/event-stream", true},
		{"application/json", false},
		{"text/plain", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isEventStream(tc.ct); got != tc.want {
			t.Errorf("isEventStream(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}
