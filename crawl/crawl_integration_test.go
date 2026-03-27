package crawl

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	sitemap "github.com/dreamsxin/go-sitemap"
	"go.uber.org/zap"
)

// buildTestServer creates an httptest.Server that serves a small site:
//
//	/          → links to /a and /b
//	/a         → links to /a/sub
//	/b         → no outgoing links
//	/a/sub     → no outgoing links
//	/external  → link to http://other.com (should be ignored)
//	/redirect  → 301 → /b
func buildTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `<html><body>
			<a href="/a">a</a>
			<a href="/b">b</a>
		</body></html>`)
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body><a href="/a/sub">sub</a></body></html>`)
	})
	mux.HandleFunc("/a/sub", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body>deep</body></html>`)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body>b</body></html>`)
	})
	mux.HandleFunc("/external", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<html><body><a href="http://other.com/page">ext</a></body></html>`)
	})
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/b", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprintf(w, `<html><body>slow</body></html>`)
	})

	return httptest.NewServer(mux)
}

func nopLogger() *zap.Logger { return zap.NewNop() }

// TestCrawlDomain_BasicDiscovery verifies that the crawler discovers all pages
// reachable from the root within the same domain.
func TestCrawlDomain_BasicDiscovery(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	sm, err := CrawlDomain(srv.URL,
		SetMaxConcurrency(2),
		SetLogger(nopLogger()),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	urls := sm.GetURLS()
	want := []string{"/a", "/b", "/a/sub"}
	for _, suffix := range want {
		found := false
		for u := range urls {
			if strings.HasSuffix(u, suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected URL with suffix %q in sitemap, got keys: %v", suffix, mapKeys(urls))
		}
	}
}

// TestCrawlDomain_ExternalLinksIgnored ensures that links pointing to a
// different host are not added to the sitemap.
func TestCrawlDomain_ExternalLinksIgnored(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	sm, err := CrawlDomain(srv.URL+"/external",
		SetLogger(nopLogger()),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}
	for u := range sm.GetURLS() {
		if strings.Contains(u, "other.com") {
			t.Errorf("external URL should not appear in sitemap: %q", u)
		}
	}
}

// TestCrawlDomain_Redirect verifies that a redirect response is followed
// and the redirect target URL is added to the sitemap.
func TestCrawlDomain_Redirect(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	sm, err := CrawlDomain(srv.URL+"/redirect",
		SetLogger(nopLogger()),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	urls := sm.GetURLS()
	found := false
	for u := range urls {
		if strings.HasSuffix(u, "/b") {
			found = true
		}
	}
	if !found {
		t.Errorf("redirect target /b not found in sitemap; keys: %v", mapKeys(urls))
	}
}

// TestCrawlDomain_CrawlValidator verifies that the CrawlValidator can skip
// URLs based on their sitemap metadata (e.g. recently modified pages).
func TestCrawlDomain_CrawlValidator(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	// Pre-populate /a as recently crawled so validator will skip it.
	recent := time.Now()
	preloaded := map[string]*sitemap.URL{
		srv.URL + "/a": {Loc: srv.URL + "/a", LastMod: &recent},
	}

	skipped := []string{}
	validator := CrawlValidator(func(u *sitemap.URL) bool {
		if u != nil && u.LastMod != nil && time.Since(*u.LastMod) < time.Hour {
			skipped = append(skipped, u.Loc)
			return false
		}
		return true
	})

	_, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
		SetSitemapURLS(preloaded),
		SetCrawlValidator(validator),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	found := false
	for _, s := range skipped {
		if strings.HasSuffix(s, "/a") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /a to be skipped by validator, skipped: %v", skipped)
	}
}

// TestCrawlDomain_URLValidator verifies that a UrlValidator can exclude URLs
// based on path patterns.
func TestCrawlDomain_URLValidator(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	// Reject any URL whose path contains "/a"
	sm, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
		SetUrlValidator(UrlValidatorFunc(func(u *url.URL) bool {
			return !strings.Contains(u.Path, "/a")
		})),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	for u := range sm.GetURLS() {
		if strings.Contains(u, "/a") {
			t.Errorf("URL %q should have been filtered by UrlValidator", u)
		}
	}
}

// TestCrawlDomain_Timeout verifies that the crawler respects the crawl timeout
// and returns a partial (non-nil) result without deadlocking.
//
// The crawl timeout signals workers to skip new URLs; in-flight HTTP requests
// complete normally. We verify: no deadlock, no error, non-nil sitemap.
func TestCrawlDomain_Timeout(t *testing.T) {
	mux := http.NewServeMux()
	// Root links to many pages so the queue stays full when timeout fires.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		sb.WriteString("<html><body>")
		for i := 0; i < 50; i++ {
			fmt.Fprintf(&sb, `<a href="/p%d">p%d</a>`, i, i)
		}
		sb.WriteString("</body></html>")
		fmt.Fprint(w, sb.String())
	})
	for i := 0; i < 50; i++ {
		i := i
		mux.HandleFunc(fmt.Sprintf("/p%d", i), func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `<html><body>page %d</body></html>`, i)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sm, err := CrawlDomain(srv.URL,
			SetLogger(nopLogger()),
			SetCrawlTimeout(100*time.Millisecond),
			SetMaxConcurrency(2),
		)
		if err != nil {
			t.Errorf("CrawlDomain error: %v", err)
		}
		if sm == nil {
			t.Error("expected non-nil sitemap")
		}
	}()

	select {
	case <-done:
		// completed without deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("CrawlDomain with timeout deadlocked (10s exceeded)")
	}
}

// TestCrawlDomain_Priority verifies that GetPriority assigns correct values
// to URLs discovered at different depths.
func TestCrawlDomain_Priority(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	sm, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	urls := sm.GetURLS()

	type wantPriority struct {
		suffix   string
		priority float32
	}
	cases := []wantPriority{
		{"/a", 0.8},
		{"/b", 0.8},
		{"/a/sub", 0.6},
	}
	for _, tc := range cases {
		for u, meta := range urls {
			if strings.HasSuffix(u, tc.suffix) {
				if meta.Priority != tc.priority {
					t.Errorf("URL %q priority = %v, want %v", u, meta.Priority, tc.priority)
				}
				break
			}
		}
	}
}

// TestCrawlDomain_EventCallback verifies the EventCallbackReadLink is invoked
// for discovered links.
func TestCrawlDomain_EventCallback(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	discovered := make(map[string]bool)
	cb := EventCallbackReadLink(func(pageURL *url.URL, _ *LinkReader) {
		discovered[pageURL.String()] = true
	})

	_, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
		SetEventCallbackReadLink(cb),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	if len(discovered) == 0 {
		t.Error("EventCallbackReadLink was never called")
	}
}

// TestCrawlDomain_SitemapURLS verifies that pre-populated SitemapURLS
// are included in the final output and re-crawled.
func TestCrawlDomain_SitemapURLS(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	ts := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	preloaded := map[string]*sitemap.URL{
		srv.URL + "/b": {Loc: srv.URL + "/b", LastMod: &ts},
	}

	sm, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
		SetSitemapURLS(preloaded),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	urls := sm.GetURLS()
	entry, ok := urls[srv.URL+"/b"]
	if !ok {
		t.Fatalf("/b not in sitemap")
	}
	// The entry should exist (may have updated LastMod after re-crawl)
	if entry.Loc != srv.URL+"/b" {
		t.Errorf("Loc = %q", entry.Loc)
	}
}

// TestCrawlDomain_CustomDomainValidator verifies a custom domain validator
// can restrict crawling to specific URL patterns.
func TestCrawlDomain_CustomDomainValidator(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	// Only allow URLs whose path starts with /a
	allowOnly := DomainValidatorFunc(func(root, link *url.URL) bool {
		if root.Host != link.Host {
			return false
		}
		return link.Path == "" || link.Path == "/" || strings.HasPrefix(link.Path, "/a")
	})

	sm, err := CrawlDomain(srv.URL,
		SetLogger(nopLogger()),
		SetDomainValidator(allowOnly),
	)
	if err != nil {
		t.Fatalf("CrawlDomain error: %v", err)
	}

	for u := range sm.GetURLS() {
		parsed, _ := url.Parse(u)
		if parsed.Path != "" && parsed.Path != "/" && !strings.HasPrefix(parsed.Path, "/a") {
			t.Errorf("URL %q should have been filtered by custom domain validator", u)
		}
	}
}

// TestCrawlDomainWithURL verifies the URL-based entry point works identically.
func TestCrawlDomainWithURL(t *testing.T) {
	srv := buildTestServer(t)
	defer srv.Close()

	root, _ := url.Parse(srv.URL)
	sm, err := CrawlDomainWithURL(root, SetLogger(nopLogger()))
	if err != nil {
		t.Fatalf("CrawlDomainWithURL error: %v", err)
	}
	if sm == nil {
		t.Error("expected non-nil sitemap")
	}
	if len(sm.GetURLS()) == 0 {
		t.Error("expected at least one URL in sitemap")
	}
}

// TestCrawlDomain_InvalidURL verifies error handling for malformed root URLs.
func TestCrawlDomain_InvalidURL(t *testing.T) {
	_, err := CrawlDomain("://bad-url", SetLogger(nopLogger()))
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

// TestCrawlDomain_AccessedPageCount verifies accessedPageCount is incremented
// only for successfully fetched pages (not for links discovered on a page).
func TestCrawlDomain_AccessedPageCount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Root page has 3 links
		fmt.Fprintf(w, `<html><body>
			<a href="/p1">p1</a>
			<a href="/p2">p2</a>
			<a href="/p3">p3</a>
		</body></html>`)
	})
	for _, p := range []string{"/p1", "/p2", "/p3"} {
		p := p
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `<html><body>page</body></html>`)
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root, _ := url.Parse(srv.URL)
	cfg := NewConfig(SetLogger(nopLogger()))
	crawler, err := NewDomainCrawler(root, cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = crawler.Crawl()
	if err != nil {
		t.Fatal(err)
	}

	// 4 pages: root + /p1 + /p2 + /p3
	count := crawler.accessedPageCount.Load()
	if count != 4 {
		t.Errorf("accessedPageCount = %d, want 4", count)
	}
}

// ─────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────

func mapKeys(m map[string]*sitemap.URL) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
