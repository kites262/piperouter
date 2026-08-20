package logging

// This file contains the deliberately opt-in request-details recorder. The
// normal access-log path does not call any of these methods unless Capture is
// enabled, keeping the data-plane hot path allocation-free for ordinary use.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const longStringLimit = 80
const detailOverhead int64 = 512

// DetailsStore is a bounded, in-memory store for opt-in request details.
// Capacity is enforced by total retained bytes; entries are evicted oldest
// first. The enabled flag is intentionally independent from configuration so
// the UI toggle is temporary and resets on restart.
type DetailsStore struct {
	enabled  atomic.Bool
	mu       sync.RWMutex
	max      int64
	used     int64
	maxEntry int64
	bodyMax  int64
	next     uint64
	items    map[string]*DetailRecord
	order    []string
}

// DetailRecord is the internal immutable-ish representation retained by the
// store. Bodies are raw bytes so their original wire representation survives.
type DetailRecord struct {
	ID          string
	StartedAt   time.Time
	FinishedAt  time.Time
	Method      string
	RequestURL  string
	Host        string
	RemoteAddr  string
	Proto       string
	RequestHead http.Header
	RequestBody []byte
	ReqTrunc    bool

	Route        string
	Transport    string
	OutboundURL  string
	OutboundHead http.Header

	Status       int
	ResponseHead http.Header
	ResponseBody []byte
	RespTrunc    bool
	Streaming    string
	Error        string
}

// NewDetailsStore creates a disabled store with the configured byte limits.
func NewDetailsStore(maxBytes, maxEntryBytes, bodyBytes int64) *DetailsStore {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxEntryBytes < 0 {
		maxEntryBytes = 0
	}
	if bodyBytes < 0 {
		bodyBytes = 0
	}
	return &DetailsStore{max: maxBytes, maxEntry: maxEntryBytes, bodyMax: bodyBytes, items: make(map[string]*DetailRecord)}
}

func (s *DetailsStore) Enabled() bool { return s != nil && s.enabled.Load() }
func (s *DetailsStore) SetEnabled(v bool) {
	if s != nil {
		s.enabled.Store(v)
	}
}

// SetLimits applies hot-reloaded limits and evicts old entries as needed.
func (s *DetailsStore) SetLimits(maxBytes, maxEntryBytes, bodyBytes int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.max, s.maxEntry, s.bodyMax = maxBytes, maxEntryBytes, bodyBytes
	s.evictLocked()
	s.mu.Unlock()
}

func (s *DetailsStore) Limits() (maxBytes, maxEntryBytes, bodyBytes int64) {
	if s == nil {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.max, s.maxEntry, s.bodyMax
}

// Begin starts a capture. It returns nil when disabled or when no capacity is
// configured. The caller must call Finish exactly once.
func (s *DetailsStore) Begin(r *http.Request) *Capture {
	if s == nil || !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	s.next++
	id := strconv.FormatUint(s.next, 10)
	s.mu.Unlock()
	c := &Capture{store: s, record: &DetailRecord{ID: id, StartedAt: time.Now()}}
	if r != nil {
		c.record.Method = r.Method
		c.record.RequestURL = requestURL(r.URL)
		c.record.Host = r.Host
		c.record.RemoteAddr = r.RemoteAddr
		c.record.Proto = r.Proto
		c.record.RequestHead = cloneHeadersRedacted(r.Header)
	}
	return c
}

// Capture is request-local and only allocated while details capture is on.
type Capture struct {
	store         *DetailsStore
	record        *DetailRecord
	reqBuf        bytes.Buffer
	respBuf       bytes.Buffer
	reqTruncated  bool
	respTruncated bool
}

func (c *Capture) ID() string {
	if c == nil {
		return ""
	}
	return c.record.ID
}

// RequestBody wraps a body without changing streaming behavior.
func (c *Capture) RequestBody(body io.ReadCloser) io.ReadCloser {
	if c == nil || body == nil {
		return body
	}
	return &captureReadCloser{ReadCloser: body, dst: &c.reqBuf, limit: c.bodyLimit(), truncated: &c.reqTruncated}
}

func (c *Capture) SetRoute(route, transport string) {
	if c != nil {
		c.record.Route, c.record.Transport = route, transport
	}
}

func (c *Capture) SetOutbound(req *http.Request) {
	if c == nil || req == nil {
		return
	}
	c.record.OutboundURL = requestURL(req.URL)
	c.record.OutboundHead = cloneHeadersRedacted(req.Header)
}

func (c *Capture) ResponseHeaders(h http.Header) {
	if c != nil {
		c.record.ResponseHead = cloneHeadersRedacted(h)
	}
}
func (c *Capture) ResponseBody(p []byte) {
	if c != nil {
		if appendCapped(&c.respBuf, p, c.bodyLimit()) {
			c.respTruncated = true
		}
	}
}
func (c *Capture) SetResult(status int, streaming, errClass string) {
	if c != nil {
		c.record.Status, c.record.Streaming, c.record.Error = status, streaming, errClass
	}
}

// Finish commits the capture to the bounded store.
func (c *Capture) Finish() {
	if c == nil || c.store == nil || c.record == nil {
		return
	}
	c.record.FinishedAt = time.Now()
	c.record.RequestBody = append([]byte(nil), c.reqBuf.Bytes()...)
	c.record.ResponseBody = append([]byte(nil), c.respBuf.Bytes()...)
	c.record.ReqTrunc = c.reqTruncated
	c.record.RespTrunc = c.respTruncated
	s := c.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.max <= 0 || s.maxEntry <= 0 {
		return
	}
	size := detailSize(c.record)
	if size > s.maxEntry { // retain no oversized item; its summary says unavailable
		return
	}
	for s.used+size > s.max && len(s.order) > 0 {
		s.removeOldestLocked()
	}
	if s.used+size > s.max {
		return
	}
	s.items[c.record.ID] = c.record
	s.order = append(s.order, c.record.ID)
	s.used += size
}

func (s *DetailsStore) removeOldestLocked() {
	if len(s.order) == 0 {
		return
	}
	id := s.order[0]
	s.order = s.order[1:]
	if old := s.items[id]; old != nil {
		s.used -= detailSize(old)
		delete(s.items, id)
	}
}

func (s *DetailsStore) evictLocked() {
	for len(s.order) > 0 {
		if s.max <= 0 || s.used <= s.max {
			break
		}
		s.removeOldestLocked()
	}
}

func (s *DetailsStore) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.items = make(map[string]*DetailRecord)
	s.order = nil
	s.used = 0
	s.mu.Unlock()
}

func (s *DetailsStore) Has(id string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.items[id]
	return ok
}

// DetailView is the JSON-safe response returned by the details endpoint.
type DetailView struct {
	ID                    string      `json:"id"`
	StartedAt             time.Time   `json:"started_at"`
	FinishedAt            time.Time   `json:"finished_at"`
	Method                string      `json:"method"`
	RequestURL            string      `json:"request_url"`
	Host                  string      `json:"host"`
	RemoteAddr            string      `json:"remote_addr"`
	Proto                 string      `json:"proto"`
	RequestHeaders        http.Header `json:"request_headers"`
	RequestBody           any         `json:"request_body,omitempty"`
	RequestBodyBytes      int         `json:"request_body_bytes"`
	RequestBodyTruncated  bool        `json:"request_body_truncated"`
	Route                 string      `json:"route"`
	Transport             string      `json:"transport"`
	OutboundURL           string      `json:"outbound_url"`
	OutboundHeaders       http.Header `json:"outbound_headers,omitempty"`
	Status                int         `json:"status"`
	ResponseHeaders       http.Header `json:"response_headers,omitempty"`
	ResponseBody          any         `json:"response_body,omitempty"`
	ResponseBodyBytes     int         `json:"response_body_bytes"`
	ResponseBodyTruncated bool        `json:"response_body_truncated"`
	Streaming             string      `json:"streaming"`
	Error                 string      `json:"error"`
}

// View returns a compact, JSON-aware representation. Long strings are
// represented by a prefix/suffix and fetched separately through Value.
func (s *DetailsStore) View(id string) (*DetailView, bool) {
	s.mu.RLock()
	r := s.items[id]
	s.mu.RUnlock()
	if r == nil {
		return nil, false
	}
	return viewRecord(r), true
}

func viewRecord(r *DetailRecord) *DetailView {
	return &DetailView{ID: r.ID, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Method: r.Method, RequestURL: r.RequestURL, Host: r.Host, RemoteAddr: r.RemoteAddr, Proto: r.Proto, RequestHeaders: r.RequestHead, RequestBody: previewBody(r.RequestBody, "request"), RequestBodyBytes: len(r.RequestBody), RequestBodyTruncated: r.ReqTrunc, Route: r.Route, Transport: r.Transport, OutboundURL: r.OutboundURL, OutboundHeaders: r.OutboundHead, Status: r.Status, ResponseHeaders: r.ResponseHead, ResponseBody: previewBody(r.ResponseBody, "response"), ResponseBodyBytes: len(r.ResponseBody), ResponseBodyTruncated: r.RespTrunc, Streaming: r.Streaming, Error: r.Error}
}

// Value returns a complete long string selected by side/index, e.g. request:0.
func (s *DetailsStore) Value(id, ref string) (string, bool) {
	s.mu.RLock()
	r := s.items[id]
	s.mu.RUnlock()
	if r == nil {
		return "", false
	}
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return "", false
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		return "", false
	}
	var raw []byte
	if parts[0] == "request" {
		raw = r.RequestBody
	} else if parts[0] == "response" {
		raw = r.ResponseBody
	} else {
		return "", false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		if idx == 0 {
			return string(raw), true
		}
		return "", false
	}
	vals := make([]string, 0)
	collectLongStrings(v, &vals)
	if idx >= len(vals) {
		return "", false
	}
	return vals[idx], true
}

func previewBody(raw []byte, side string) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		n := 0
		return previewValue(v, side, &n)
	}
	text := string(raw)
	if len(text) > longStringLimit {
		return longStringPreview(text, side+":0")
	}
	return text
}

func previewValue(v any, side string, n *int) any {
	switch x := v.(type) {
	case string:
		if len(x) > longStringLimit {
			ref := side + ":" + strconv.Itoa(*n)
			(*n)++
			return longStringPreview(x, ref)
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = previewValue(x[i], side, n)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := x[k]
			out[k] = previewValue(v, side, n)
		}
		return out
	default:
		return v
	}
}

func collectLongStrings(v any, out *[]string) {
	switch x := v.(type) {
	case string:
		if len(x) > longStringLimit {
			*out = append(*out, x)
		}
	case []any:
		for _, v := range x {
			collectLongStrings(v, out)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectLongStrings(x[k], out)
		}
	}
}

func longStringPreview(s, ref string) map[string]any {
	prefix, suffix := stringEdges(s, 10)
	return map[string]any{"type": "long_string", "length": len(s), "prefix": prefix, "suffix": suffix, "value_ref": ref}
}

func stringEdges(s string, n int) (string, string) {
	r := []rune(s)
	if len(r) <= n*2 {
		return s, s
	}
	return string(r[:n]), string(r[len(r)-n:])
}

func requestURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func cloneHeadersRedacted(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		if sensitiveHeader(k) {
			out[k] = []string{"[REDACTED]"}
		} else {
			out[k] = append([]string(nil), v...)
		}
	}
	return out
}
func sensitiveHeader(k string) bool {
	switch strings.ToLower(k) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key":
		return true
	}
	return false
}
func detailSize(r *DetailRecord) int64 {
	n := detailOverhead + int64(len(r.RequestBody)+len(r.ResponseBody))
	n += int64(len(r.Method) + len(r.RequestURL) + len(r.Host) + len(r.RemoteAddr) + len(r.Proto) + len(r.Route) + len(r.Transport) + len(r.OutboundURL) + len(r.Streaming) + len(r.Error))
	for k, v := range r.RequestHead {
		n += int64(len(k))
		for _, x := range v {
			n += int64(len(x))
		}
	}
	for k, v := range r.ResponseHead {
		n += int64(len(k))
		for _, x := range v {
			n += int64(len(x))
		}
	}
	return n
}
func (c *Capture) bodyLimit() int64 {
	if c == nil || c.store == nil {
		return 0
	}
	c.store.mu.RLock()
	n := c.store.bodyMax
	c.store.mu.RUnlock()
	if n <= 0 {
		return 0
	}
	return n
}
func appendCapped(dst *bytes.Buffer, p []byte, limit int64) bool {
	if limit <= 0 || int64(dst.Len()) >= limit {
		return len(p) > 0
	}
	left := limit - int64(dst.Len())
	truncated := false
	if int64(len(p)) > left {
		p = p[:left]
		truncated = true
	}
	_, _ = dst.Write(p)
	return truncated
}

type captureReadCloser struct {
	io.ReadCloser
	dst       *bytes.Buffer
	limit     int64
	truncated *bool
}

func (r *captureReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		if appendCapped(r.dst, p[:n], r.limit) && r.truncated != nil {
			*r.truncated = true
		}
	}
	return n, err
}
