package crawl

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sitemap "github.com/dreamsxin/go-sitemap"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────
// Config / Validate
// ─────────────────────────────────────────────

func TestNewConfig_Defaults(t *testing.T) {
	cfg := NewConfig()
	if cfg.MaxConcurrency != DefaultMaxConcurrency {
		t.Errorf("MaxConcurrency = %d, want %d", cfg.MaxConcurrency, DefaultMaxConcurrency)
	}
	if cfg.MaxPendingURLS != DefaultMaxPendingURLS {
		t.Errorf("MaxPendingURLS = %d, want %d", cfg.MaxPendingURLS, DefaultMaxPendingURLS)
	}
	if cfg.Client == nil {
		t.Error("Client must not be nil")
	}
	if cfg.Logger == nil {
		t.Error("Logger must not be nil")
	}
	if cfg.DomainValidator == nil {
		t.Error("DomainValidator must not be nil")
	}
	if cfg.Priority == nil {
		t.Error("Priority must not be nil")
	}
}

func TestConfig_Validate_AllOptions(t *testing.T) {
	logger := zap.NewNop()
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(c *Config) {}, false},
		{"MaxConcurrency=0", func(c *Config) { c.MaxConcurrency = 0 }, true},
		{"MaxConcurrency=-1", func(c *Config) { c.MaxConcurrency = -1 }, true},
		{"MaxPendingURLS=0", func(c *Config) { c.MaxPendingURLS = 0 }, true},
		{"KeepAlive<0", func(c *Config) { c.KeepAlive = -time.Second }, true},
		{"Timeout<0", func(c *Config) { c.Timeout = -time.Second }, true},
		{"Client=nil", func(c *Config) { c.Client = nil }, true},
		{"Logger=nil", func(c *Config) { c.Logger = nil }, true},
		{"DomainValidator=nil", func(c *Config) { c.DomainValidator = nil }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewConfig(SetLogger(logger))
			tc.mutate(cfg)
			err := cfg.Validate()
			if tc.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestSetOptions(t *testing.T) {
	logger := zap.NewNop()
	client := &http.Client{}
	customPriority := PriorityFunc(func(u *url.URL) float32 { return 0.5 })
	customDomain := DomainValidatorFunc(func(root, link *url.URL) bool { return true })
	customURL := UrlValidatorFunc(func(u *url.URL) bool { return true })
	customCrawl := CrawlValidator(func(u *sitemap.URL) bool { return true })
	cb := EventCallbackReadLink(func(_ *url.URL, _ *LinkReader) {})

	cfg := NewConfig(
		SetMaxConcurrency(4),
		SetMaxPendingURLS(100),
		SetCrawlTimeout(5*time.Second),
		SetKeepAlive(10*time.Second),
		SetTimeout(3*time.Second),
		SetClient(client),
		SetLogger(logger),
		SetCrawlValidator(customCrawl),
		SetDomainValidator(customDomain),
		SetUrlValidator(customURL),
		SetPriority(customPriority),
		SetEventCallbackReadLink(cb),
	)

	if cfg.MaxConcurrency != 4 {
		t.Errorf("MaxConcurrency = %d", cfg.MaxConcurrency)
	}
	if cfg.MaxPendingURLS != 100 {
		t.Errorf("MaxPendingURLS = %d", cfg.MaxPendingURLS)
	}
	if cfg.CrawlTimeout != 5*time.Second {
		t.Errorf("CrawlTimeout = %v", cfg.CrawlTimeout)
	}
	if cfg.KeepAlive != 10*time.Second {
		t.Errorf("KeepAlive = %v", cfg.KeepAlive)
	}
	if cfg.Timeout != 3*time.Second {
		t.Errorf("Timeout = %v", cfg.Timeout)
	}
	if cfg.Client == nil {
		t.Error("Client is nil")
	}
	if cfg.Logger != logger {
		t.Error("Logger not set")
	}
	if cfg.CrawlValidator == nil {
		t.Error("CrawlValidator is nil")
	}
	if cfg.DomainValidator == nil {
		t.Error("DomainValidator is nil")
	}
	if len(cfg.UrlValidators) != 1 {
		t.Errorf("UrlValidators len = %d", len(cfg.UrlValidators))
	}
	if cfg.Priority == nil {
		t.Error("Priority is nil")
	}
	if cfg.EventCallbackReadLink == nil {
		t.Error("EventCallbackReadLink is nil")
	}
}

func TestSetClient_Nil(t *testing.T) {
	cfg := NewConfig(SetClient(nil))
	// nil client triggers auto-creation in NewConfig
	if cfg.Client == nil {
		t.Error("Client should be auto-created when nil is passed")
	}
}

func TestSetSitemapURLS(t *testing.T) {
	urls := map[string]*sitemap.URL{
		"http://example.com/a": {Loc: "http://example.com/a"},
	}
	cfg := NewConfig(SetSitemapURLS(urls))
	if cfg.SitemapURLS == nil {
		t.Error("SitemapURLS is nil")
	}
	if _, ok := cfg.SitemapURLS["http://example.com/a"]; !ok {
		t.Error("SitemapURLS missing expected entry")
	}
}

// ─────────────────────────────────────────────
// GetPriority
// ─────────────────────────────────────────────

func TestGetPriority(t *testing.T) {
	cases := []struct {
		path string
		want float32
	}{
		{"/", 1.0},
		{"", 1.0},
		{"/page", 0.8},
		{"/a/b", 0.6},
		{"/a/b/c", 0.4},
		{"/a/b/c/d", 0.4},
	}
	for _, tc := range cases {
		u := &url.URL{Path: tc.path}
		got := GetPriority(u)
		if got != tc.want {
			t.Errorf("GetPriority(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestGetPriority_Nil(t *testing.T) {
	if got := GetPriority(nil); got != 0.0 {
		t.Errorf("GetPriority(nil) = %v, want 0.0", got)
	}
}

// ─────────────────────────────────────────────
// ValidateHosts
// ─────────────────────────────────────────────

func TestValidateHosts(t *testing.T) {
	root := mustParse("http://example.com/path")
	cases := []struct {
		link string
		want bool
	}{
		{"http://example.com/other", true},
		{"https://example.com/page", true},  // different scheme, same host
		{"http://other.com/page", false},
		{"http://sub.example.com/", false},
	}
	for _, tc := range cases {
		got := ValidateHosts(root, mustParse(tc.link))
		if got != tc.want {
			t.Errorf("ValidateHosts(%q) = %v, want %v", tc.link, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────
// Adapter types
// ─────────────────────────────────────────────

func TestUrlValidatorFunc(t *testing.T) {
	called := false
	v := UrlValidatorFunc(func(u *url.URL) bool {
		called = true
		return u.Path == "/ok"
	})
	u := mustParse("http://example.com/ok")
	if !v.Validate(u) {
		t.Error("expected true")
	}
	if !called {
		t.Error("validator not called")
	}
	u2 := mustParse("http://example.com/bad")
	if v.Validate(u2) {
		t.Error("expected false")
	}
}

func TestDomainValidatorFunc(t *testing.T) {
	v := DomainValidatorFunc(func(root, link *url.URL) bool {
		return root.Host == link.Host
	})
	root := mustParse("http://example.com")
	same := mustParse("http://example.com/page")
	other := mustParse("http://other.com/page")
	if !v.Validate(root, same) {
		t.Error("expected true for same host")
	}
	if v.Validate(root, other) {
		t.Error("expected false for different host")
	}
}

func TestPriorityFunc(t *testing.T) {
	p := PriorityFunc(func(u *url.URL) float32 { return 0.7 })
	got := p.Get(mustParse("http://example.com/"))
	if got != 0.7 {
		t.Errorf("Get() = %v, want 0.7", got)
	}
}

func TestCrawlValidator(t *testing.T) {
	v := CrawlValidator(func(u *sitemap.URL) bool {
		return u.Loc == "http://example.com/"
	})
	if !v(&sitemap.URL{Loc: "http://example.com/"}) {
		t.Error("expected true")
	}
	if v(&sitemap.URL{Loc: "http://example.com/other"}) {
		t.Error("expected false")
	}
}

// ─────────────────────────────────────────────
// SiteMap
// ─────────────────────────────────────────────

func newTestSiteMap(root string) *SiteMap {
	u := mustParse(root)
	return NewSiteMap(u,
		DomainValidatorFunc(ValidateHosts),
		nil,
		PriorityFunc(GetPriority),
	)
}

func TestSiteMap_AppendURL_NewURL(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	if !sm.appendURL(u) {
		t.Error("appendURL should return true for new URL")
	}
	if _, ok := sm.sitemapURLS["http://example.com/page"]; !ok {
		t.Error("URL not stored in sitemapURLS")
	}
}

func TestSiteMap_AppendURL_Duplicate(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	sm.appendURL(u)
	if sm.appendURL(u) {
		t.Error("appendURL should return false for duplicate")
	}
}

func TestSiteMap_AppendURL_ExternalDomain(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://other.com/page")
	if sm.appendURL(u) {
		t.Error("appendURL should return false for external domain")
	}
}

func TestSiteMap_AppendURL_TrailingSlash(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u1 := mustParse("http://example.com/page/")
	u2 := mustParse("http://example.com/page")
	sm.appendURL(u1)
	if sm.appendURL(u2) {
		t.Error("URL with/without trailing slash should be treated as duplicate")
	}
}

func TestSiteMap_AppendURL_URLValidator(t *testing.T) {
	root := mustParse("http://example.com")
	sm := NewSiteMap(root,
		DomainValidatorFunc(ValidateHosts),
		[]UrlValidator{UrlValidatorFunc(func(u *url.URL) bool {
			return !strings.Contains(u.Path, "skip")
		})},
		PriorityFunc(GetPriority),
	)
	if sm.appendURL(mustParse("http://example.com/skip-this")) {
		t.Error("URL validator should have rejected this URL")
	}
	if !sm.appendURL(mustParse("http://example.com/ok")) {
		t.Error("URL validator should have accepted this URL")
	}
}

func TestSiteMap_GetURL(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	sm.appendURL(u)

	got, ok := sm.GetURL(u)
	if !ok {
		t.Fatal("GetURL returned !ok for existing URL")
	}
	if got.Loc != "http://example.com/page" {
		t.Errorf("Loc = %q, want %q", got.Loc, "http://example.com/page")
	}
}

func TestSiteMap_GetURL_NotFound(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	_, ok := sm.GetURL(mustParse("http://example.com/missing"))
	if ok {
		t.Error("expected !ok for missing URL")
	}
}

func TestSiteMap_UpdatedMod_WithTime(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	sm.appendURL(u)

	ts := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if !sm.updatedMod(u, &ts) {
		t.Error("updatedMod should return true for existing URL")
	}
	got, _ := sm.GetURL(u)
	if got.LastMod == nil || !got.LastMod.Equal(ts) {
		t.Errorf("LastMod = %v, want %v", got.LastMod, ts)
	}
}

func TestSiteMap_UpdatedMod_NilTime_SetsNow(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	sm.appendURL(u)

	before := time.Now().Add(-time.Second)
	sm.updatedMod(u, nil)
	after := time.Now().Add(time.Second)

	got, _ := sm.GetURL(u)
	if got.LastMod == nil {
		t.Fatal("LastMod should be set")
	}
	if got.LastMod.Before(before) || got.LastMod.After(after) {
		t.Errorf("LastMod %v not in expected range [%v, %v]", got.LastMod, before, after)
	}
}

func TestSiteMap_UpdatedMod_AlreadySet_NoOverwrite(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/page")
	sm.appendURL(u)

	ts := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	sm.updatedMod(u, &ts)

	// Calling again with nil should NOT overwrite an existing LastMod
	result := sm.updatedMod(u, nil)
	if result {
		t.Error("updatedMod should return false when LastMod already set and no new time provided")
	}
	got, _ := sm.GetURL(u)
	if !got.LastMod.Equal(ts) {
		t.Errorf("LastMod was overwritten: got %v, want %v", got.LastMod, ts)
	}
}

func TestSiteMap_UpdatedMod_MissingURL(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	u := mustParse("http://example.com/missing")
	if sm.updatedMod(u, nil) {
		t.Error("updatedMod should return false for missing URL")
	}
}

func TestSiteMap_UpdatedMod_TrailingSlash(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	// Store without trailing slash
	sm.appendURL(mustParse("http://example.com/page"))
	// Update with trailing slash - should still find it
	ts := time.Now()
	if !sm.updatedMod(mustParse("http://example.com/page/"), &ts) {
		t.Error("updatedMod should match URL ignoring trailing slash")
	}
}

func TestSiteMap_GetURLS_ReturnsCopy(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	sm.appendURL(mustParse("http://example.com/a"))
	sm.appendURL(mustParse("http://example.com/b"))

	snapshot := sm.GetURLS()
	if len(snapshot) != 2 {
		t.Errorf("GetURLS returned %d entries, want 2", len(snapshot))
	}
	// Modifying the copy must not affect internal state
	delete(snapshot, "http://example.com/a")
	if len(sm.GetURLS()) != 2 {
		t.Error("Modifying returned map affected internal state")
	}
}

func TestSiteMap_WriteMap(t *testing.T) {
	sm := newTestSiteMap("http://example.com")
	sm.appendURL(mustParse("http://example.com/a"))
	sm.appendURL(mustParse("http://example.com/b"))

	var buf bytes.Buffer
	sm.WriteMap(&buf)
	out := buf.String()
	if !strings.Contains(out, "http://example.com/a") {
		t.Error("WriteMap missing /a")
	}
	if !strings.Contains(out, "http://example.com/b") {
		t.Error("WriteMap missing /b")
	}
}

// ─────────────────────────────────────────────
// LinkReader
// ─────────────────────────────────────────────

func TestLinkReader_URL(t *testing.T) {
	u := mustParse("http://example.com/page")
	lr := NewLinkReader(u, &http.Client{}, NewConfig())
	if lr.URL() != "http://example.com/page" {
		t.Errorf("URL() = %q", lr.URL())
	}
}

func TestLinkReader_GetBody_BeforeFetch(t *testing.T) {
	u := mustParse("http://example.com/page")
	lr := NewLinkReader(u, &http.Client{}, NewConfig())
	if lr.GetBody() != nil {
		t.Error("GetBody should be nil before first Read()")
	}
}

func TestLinkReader_SetLastModTime(t *testing.T) {
	u := mustParse("http://example.com/page")
	lr := NewLinkReader(u, &http.Client{}, NewConfig())
	ts := time.Now()
	lr.SetLastModTime(&ts)
	if lr.lastModTime == nil || !lr.lastModTime.Equal(ts) {
		t.Error("SetLastModTime did not store the time")
	}
}

func TestLinkReader_Read_Done(t *testing.T) {
	u := mustParse("http://example.com/page")
	lr := NewLinkReader(u, &http.Client{}, NewConfig())
	lr.done = true
	_, err := lr.Read()
	if err != io.EOF {
		t.Errorf("Read() on done reader = %v, want io.EOF", err)
	}
}

func TestLinkReader_Read_HTTPError(t *testing.T) {
	u := mustParse("http://127.0.0.1:1") // nothing listening
	client := &http.Client{Timeout: 100 * time.Millisecond}
	lr := NewLinkReader(u, client, NewConfig())
	_, err := lr.Read()
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

// ─────────────────────────────────────────────
// NewDomainCrawler validation
// ─────────────────────────────────────────────

func TestNewDomainCrawler_InvalidConfig(t *testing.T) {
	root := mustParse("http://example.com")
	cfg := NewConfig()
	cfg.MaxConcurrency = 0 // invalid
	_, err := NewDomainCrawler(root, cfg)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

// ─────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────

func mustParse(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
