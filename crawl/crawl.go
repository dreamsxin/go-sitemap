// Package crawl implements the core web crawling engine for automatic sitemap generation.

package crawl

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dreamsxin/go-sitemap"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"golang.org/x/net/html"
)

// hrefAttr is the byte slice used to match the 'href' attribute in HTML anchor tags.
// Using a pre-allocated byte slice avoids repeated allocations during link parsing.
var hrefAttr = []byte("href")

// CrawlDomain crawls all pages within a domain starting from the provided root URL.
//
// This is the main entry point for the crawler. It accepts a URL string and
// optional configuration options, then discovers and catalogs all reachable
// pages within the same domain.
//
// Parameters:
//   - rootURL: Starting URL for the crawl (e.g., "https://example.com")
//   - opts: Optional configuration options (SetMaxConcurrency, SetTimeout, etc.)
//
// Returns:
//   - *SiteMap: The generated sitemap containing all discovered URLs
//   - error: Any error encountered during crawling
//
// Example:
//
//	siteMap, err := crawl.CrawlDomain("https://example.com",
//	    crawl.SetMaxConcurrency(16),
//	    crawl.SetTimeout(30*time.Second),
//	)
func CrawlDomain(rootURL string, opts ...Option) (*SiteMap, error) {
	root, rootErr := url.Parse(rootURL)
	if rootErr != nil {
		return nil, rootErr
	}
	return CrawlDomainWithURL(root, opts...)
}

// CrawlDomainWithURL crawls all pages within a domain starting from the provided URL.
//
// This function is similar to CrawlDomain but accepts a parsed *url.URL instead
// of a string. Use this when you already have a parsed URL or need to manipulate
// the URL before crawling.
//
// Parameters:
//   - root: Parsed root URL to start crawling from
//   - opts: Optional configuration options
//
// Returns:
//   - *SiteMap: The generated sitemap with all discovered URLs
//   - error: Any error encountered during crawling
func CrawlDomainWithURL(root *url.URL, opts ...Option) (*SiteMap, error) {
	config := NewConfig(opts...)

	crawler, crawlerError := NewDomainCrawler(root, config)
	if crawlerError != nil {
		return nil, crawlerError
	}

	return crawler.Crawl()
}

// DomainCrawler manages the state and execution of a domain-wide web crawl.
//
// The crawler uses a concurrent architecture with multiple goroutines processing
// URLs from a shared queue. It maintains:
//   - A channel of pending URLs to crawl
//   - A wait group to track in-progress crawl operations
//   - Atomic counters for pages accessed and timeout state
//
// Each DomainCrawler instance is designed for a single crawl operation.
// For multiple crawls, create separate DomainCrawler instances.
type DomainCrawler struct {
	root                 *url.URL        // Root URL defining the crawl domain boundary
	config               *Config         // Crawler configuration
	siteMap              *SiteMap        // Accumulated sitemap results
	pendingURLS          chan *url.URL   // Queue of URLs waiting to be crawled
	pendingURLSRemaining *sync.WaitGroup // Tracks active crawl operations
	accessedPageCount    atomic.Uint64   // Count of successfully accessed pages
	timedOut             atomic.Bool     // Flag indicating if crawl timeout was reached
	closed               bool            // Flag indicating if pendingURLS channel is closed
	mu                   sync.Mutex      // Protects closed flag and channel writes
}

// NewDomainCrawler creates and initializes a new DomainCrawler instance.
//
// This function validates the configuration and sets up the initial state:
//   - Creates the URL processing channel with configured buffer size
//   - Initializes the SiteMap with domain and URL validators
//   - Seeds the queue with the root URL (and any pre-existing sitemap URLs)
//
// Parameters:
//   - root: Root URL defining the crawl domain
//   - config: Validated crawler configuration
//
// Returns:
//   - *DomainCrawler: Initialized crawler ready to execute
//   - error: Configuration validation error, if any
func NewDomainCrawler(root *url.URL, config *Config) (*DomainCrawler, error) {
	configError := config.Validate()
	if configError != nil {
		return nil, configError
	}

	siteMap := NewSiteMap(root, config.DomainValidator, config.UrlValidators, config.Priority)

	pendingURLS := make(chan *url.URL, config.MaxPendingURLS)
	pendingURLS <- root

	var pendingURLSRemaining sync.WaitGroup
	pendingURLSRemaining.Add(1)

	crawler := &DomainCrawler{
		root:                 root,
		config:               config,
		siteMap:              siteMap,
		pendingURLS:          pendingURLS,
		pendingURLSRemaining: &pendingURLSRemaining,
	}

	if config.SitemapURLS != nil {
		siteMap.sitemapURLS = config.SitemapURLS

		// Seed the pending queue in a goroutine to avoid deadlock when the
		// number of pre-existing URLs exceeds the channel buffer capacity.
		pendingURLSRemaining.Add(1)
		go func() {
			defer pendingURLSRemaining.Done()
			for _, sitemapURL := range config.SitemapURLS {
				siteMap.siteURLS[sitemapURL.Loc] = true
				loc, err := url.Parse(sitemapURL.Loc)
				if err != nil {
					continue
				}
				crawler.mu.Lock()
				if !crawler.closed {
					select {
					case pendingURLS <- loc:
						pendingURLSRemaining.Add(1)
					default:
						config.Logger.Error("too many sitemap urls, url will be ignored",
							zap.String("url", sitemapURL.Loc),
						)
					}
				}
				crawler.mu.Unlock()
			}
		}()
	}

	return crawler, nil
}

// Crawl executes the domain crawl and returns the generated sitemap.
//
// This method:
//  1. Spawns worker goroutines (number configured by MaxConcurrency)
//  2. Optionally starts a timeout goroutine if CrawlTimeout is set
//  3. Waits for all URLs to be processed
//  4. Returns the accumulated sitemap
//
// The crawl uses a producer-consumer pattern:
//   - Worker goroutines consume URLs from the pending channel
//   - Each worker discovers new links and adds them to the queue
//   - The process continues until all URLs are processed or timeout occurs
//
// Important: Crawl is NOT thread-safe. Each caller must create a separate
// DomainCrawler instance. Concurrent calls on the same crawler will cause
// race conditions.
//
// Returns:
//   - *SiteMap: Generated sitemap with all discovered URLs
//   - error: Error if no pages could be accessed
func (crawler *DomainCrawler) Crawl() (*SiteMap, error) {
	maxConcurrency := crawler.config.MaxConcurrency
	crawlTimeout := crawler.config.CrawlTimeout

	for i := 0; i < maxConcurrency; i++ {
		go crawler.drainURLS()
	}

	// The timeout mechanism signals to the goroutines to stop reading
	// more URLs after the specified timeout. The function doesn't
	// return until the goroutines have drained the URLs.
	if crawlTimeout > 0 {
		go func() {
			time.Sleep(crawlTimeout)
			crawler.timedOut.Store(true)
		}()
	}

	crawler.pendingURLSRemaining.Wait()
	crawler.mu.Lock()
	close(crawler.pendingURLS)
	crawler.closed = true
	crawler.mu.Unlock()

	if crawler.accessedPageCount.Load() == 0 {
		return nil, fmt.Errorf("unable to access url %s", crawler.root.String())
	}

	return crawler.siteMap, nil
}

// drainURLS is the worker goroutine that processes URLs from the pending queue.
//
// Each worker:
//  1. Reads URLs from the pendingURLS channel
//  2. Checks if the URL should be skipped (timeout or validator failure)
//  3. Fetches the page and extracts all links
//  4. Updates the sitemap with last-modified time if available
//  5. Signals completion via pendingURLSRemaining
//
// Workers run until the channel is closed and all URLs are processed.
// The timeout flag prevents new URLs from being crawled after the deadline,
// but allows in-progress crawls to complete.
func (crawler *DomainCrawler) drainURLS() {
	client := crawler.config.Client
	logger := crawler.config.Logger
	validator := crawler.config.CrawlValidator
	for pageURL := range crawler.pendingURLS {
		func() {
			defer crawler.pendingURLSRemaining.Done()

			logger.Debug("crawling page for links",
				zap.String("url", pageURL.String()),
			)

			siteurl, ok := crawler.siteMap.GetURL(pageURL)
			if !ok {
				logger.Error("crawling page for links: url not found in sitemap",
					zap.String("url", pageURL.String()),
				)
			}
			if validator != nil && !validator(siteurl) {
				logger.Debug("skipping url due to validator", zap.String("url", pageURL.String()))
				return
			}
			if crawler.timedOut.Load() {
				logger.Debug("skipping url due to timeout",
					zap.String("url", pageURL.String()),
				)
				return
			}
			if ok {
				crawler.siteMap.updatedMod(pageURL, nil)
			}
			linkReader := NewLinkReader(pageURL, client, crawler.config)
			crawler.readAllLinks(linkReader)
			if linkReader.content != nil {
				crawler.accessedPageCount.Add(1)
			}
			if linkReader.lastModTime != nil {
				crawler.siteMap.updatedMod(pageURL, linkReader.lastModTime)
			}
		}()
	}
}

// readAllLinks reads all links from a page and queues unseen URLs for crawling.
//
// This method:
//  1. Iterates through all links discovered by the LinkReader
//  2. Parses and resolves each link relative to the current page
//  3. Checks if the link is new (not already crawled)
//  4. Adds new links to the pending queue (if not full)
//  5. Invokes the EventCallbackReadLink if configured
//
// The method handles errors gracefully:
//   - Parse errors are logged and skipped
//   - Read errors (except EOF) are logged as warnings
//   - Queue overflow is logged as an error and the URL is dropped
//
// Parameters:
//   - linkReader: LinkReader instance for the current page
func (crawler *DomainCrawler) readAllLinks(linkReader *LinkReader) {
	logger := crawler.config.Logger
	callback := crawler.config.EventCallbackReadLink

	for {
		hrefString, hrefErr := linkReader.Read()

		if hrefErr != nil {
			if hrefErr != io.EOF {
				// TODO: If we error while reading a page we could schedule
				// it for retry. We would then need to configure some sort
				// of max attempts and perhaps some sort of backoff to
				// prevent spamming the page with requests.
				logger.Warn("error reading link from channel",
					zap.String("page", linkReader.URL()),
					zap.Error(hrefErr),
				)
			}
			break
		}

		hrefURL, hrefParseErr := url.Parse(hrefString)
		if hrefParseErr != nil {
			logger.Warn("error parsing url",
				zap.String("page", linkReader.URL()),
				zap.String("link", hrefString),
				zap.Error(hrefParseErr),
			)

			continue
		}

		// Note that the link must be resolved relative to the current
		// page. URLs such as "?a=123" are rooted in the current path
		hrefResolved := linkReader.pageURL.ResolveReference(hrefURL)

		if crawler.siteMap.appendURL(hrefResolved) {
			logger.Debug("found new page",
				zap.String("page", hrefResolved.String()),
			)
			// Note that if we were to do blocking writes here, the
			// buffered channel could be full and the write would block
			// here. If all goroutines were blocked on writing to the
			// channel this would deadlock.
			// Also, we need to check if the channel has been closed to
			// avoid panic when sending to a closed channel.
			crawler.mu.Lock()
			if !crawler.closed {
				select {
				case crawler.pendingURLS <- hrefResolved:
					logger.Debug("page appended to channel",
						zap.String("page", hrefResolved.String()),
					)
					crawler.pendingURLSRemaining.Add(1)
				default:
					// If the buffered channel is full we ran out of memory
					logger.Error("too many pending urls, page will be ignored",
						zap.String("page", hrefResolved.String()),
						zap.String("link", linkReader.URL()),
					)
				}
			} else {
				logger.Debug("channel closed, skipping new url",
					zap.String("page", hrefResolved.String()),
				)
			}
			crawler.mu.Unlock()
			if callback != nil {
				callback(hrefResolved, linkReader)
			}
		}
	}
}

// CrawlValidator is a function type for validating whether a URL should be crawled
// based on its sitemap.URL metadata (e.g., priority, last-modified time).
//
// Return true to crawl the URL, false to skip it.
type CrawlValidator func(pageURL *sitemap.URL) bool

// UrlValidator is an interface for validating discovered URLs before crawling.
//
// Implementations can filter URLs based on custom criteria such as:
//   - Presence of query parameters
//   - Presence of URL fragments
//   - Path patterns
//   - Custom business logic
type UrlValidator interface {
	Validate(link *url.URL) bool
}

// UrlValidatorFunc is a function adapter that implements the UrlValidator interface.
// This allows ordinary functions to be used as URL validators.
type UrlValidatorFunc func(link *url.URL) bool

// Validate calls v(link) to validate the URL.
// This method satisfies the UrlValidator interface.
func (v UrlValidatorFunc) Validate(link *url.URL) bool {
	return v(link)
}

// DomainValidator is an interface for validating that discovered URLs belong
// to the same domain as the root URL.
//
// Implementations can use various strategies:
//   - Simple host comparison (default)
//   - Scheme-aware validation (http vs https)
//   - Subdomain handling
//   - DNS-based validation
type DomainValidator interface {
	Validate(root *url.URL, link *url.URL) bool
}

// DomainValidatorFunc is a function adapter that implements the DomainValidator interface.
// This allows ordinary functions to be used as domain validators.
type DomainValidatorFunc func(root, link *url.URL) bool

// Validate calls v(root, link) to check if the link belongs to the same domain.
// This method satisfies the DomainValidator interface.
func (v DomainValidatorFunc) Validate(root, link *url.URL) bool {
	return v(root, link)
}

// ValidateHosts is the default domain validation function.
//
// It compares only the host component of URLs, ignoring:
//   - Scheme (http vs https)
//   - Port numbers
//   - Path, query, and fragment components
//
// This provides a simple, efficient check that works for most use cases.
//
// Parameters:
//   - root: The root URL defining the domain boundary
//   - link: The discovered link to validate
//
// Returns:
//   - true if both URLs have the same host, false otherwise
func ValidateHosts(root, link *url.URL) bool {
	// Note: We could consider the transport to also be relevant (http vs https)
	return root.Host == link.Host
}

// Priority is an interface for calculating URL priority values.
//
// Priority values range from 0.0 to 1.0 and indicate the relative importance
// of a URL compared to other URLs on the site. Search engines use this as
// a hint for crawl prioritization.
type Priority interface {
	Get(link *url.URL) float32
}

// PriorityFunc is a function adapter that implements the Priority interface.
// This allows ordinary functions to be used as priority calculators.
type PriorityFunc func(link *url.URL) float32

// Get calls p(link) to calculate the priority value.
// This method satisfies the Priority interface.
func (p PriorityFunc) Get(link *url.URL) float32 {
	return p(link)
}

// GetPriority calculates a default priority based on URL path depth.
//
// The algorithm assigns higher priority to shallower URLs:
//   - Root (/): 1.0 - Highest priority
//   - First level (/*): 0.8 - High priority
//   - Second level (/*/*): 0.6 - Medium priority
//   - Deeper (/*/*/*+): 0.4 - Lower priority
//
// This reflects the common pattern where important content is closer to
// the root of the site hierarchy.
//
// Parameters:
//   - link: URL to calculate priority for
//
// Returns:
//   - float32: Priority value between 0.0 and 1.0
func GetPriority(link *url.URL) float32 {
	if link == nil {
		return 0.0
	}
	num := 0
	path := strings.Trim(link.Path, "/")
	if path != "" {
		num = strings.Count(path, "/") + 1
	}
	switch num {
	case 0:
		return 1.0
	case 1:
		return 0.8
	case 2:
		return 0.6
	default:
		return 0.4
	}
}

// EventCallbackReadLink is a callback function type invoked when a new link
// is discovered during crawling.
//
// The callback receives:
//   - pageURL: The resolved URL of the discovered link
//   - linkReader: The LinkReader that found the link (provides access to page content)
//
// Use cases:
//   - Extracting last-modified times from page content
//   - Real-time logging of discovered URLs
//   - Custom metadata extraction
type EventCallbackReadLink func(pageURL *url.URL, linkReader *LinkReader)

// SiteMap maintains the state of a sitemap being built during crawling.
//
// The SiteMap is thread-safe and uses a read-write mutex to allow concurrent
// access from multiple crawler goroutines. It tracks:
//   - All discovered URLs (siteURLS)
//   - Sitemap URL metadata (sitemapURLS)
//   - Domain and URL validation rules
//   - Priority calculation strategy
//
// The SiteMap is automatically populated by the DomainCrawler and can be
// exported to XML format after crawling completes.
type SiteMap struct {
	url             *url.URL                // Root URL defining the domain boundary
	rwl             *sync.RWMutex           // Read-write mutex for thread-safe access
	siteURLS        map[string]bool         // Set of all discovered URL strings
	sitemapURLS     map[string]*sitemap.URL // Map of URL strings to sitemap.URL metadata
	urlValidators   []UrlValidator          // Chain of URL validators
	domainValidator DomainValidator         // Domain boundary validator
	priority        Priority                // Priority calculation strategy
}

// NewSiteMap creates and initializes a new SiteMap instance.
//
// Parameters:
//   - url: Root URL defining the domain boundary
//   - validator: Domain validator for filtering external links
//   - urlValidators: Additional URL validators (can be nil or empty)
//   - priority: Priority calculation strategy
//
// Returns:
//   - *SiteMap: Initialized sitemap ready for crawling
func NewSiteMap(url *url.URL, validator DomainValidator, urlValidators []UrlValidator, priority Priority) *SiteMap {
	return &SiteMap{
		url:             url,
		rwl:             &sync.RWMutex{},
		siteURLS:        map[string]bool{},
		sitemapURLS:     map[string]*sitemap.URL{},
		urlValidators:   urlValidators,
		domainValidator: validator,
		priority:        priority,
	}
}

// GetURL retrieves sitemap metadata for a specific URL.
//
// This method is thread-safe and uses a read lock to allow concurrent access.
//
// Parameters:
//   - url: URL to look up
//
// Returns:
//   - *sitemap.URL: Sitemap metadata if found, nil otherwise
//   - bool: true if URL exists in the sitemap, false otherwise
func (s *SiteMap) GetURL(url *url.URL) (*sitemap.URL, bool) {
	urlString := url.String()
	urlString = strings.TrimRight(urlString, "/")

	s.rwl.RLock()
	defer s.rwl.RUnlock()
	ret, ok := s.sitemapURLS[urlString]
	return ret, ok
}

// appendURL attempts to add a URL to the sitemap for crawling.
//
// This method performs several checks:
//  1. Domain validation (must be same domain as root)
//  2. URL validation (must pass all configured validators)
//  3. Duplicate detection (skip if already crawled)
//
// The method uses a double-check locking pattern:
//   - First check with read lock (fast path for duplicates)
//   - Second check with write lock (handles race conditions)
//
// Parameters:
//   - url: URL to potentially add
//
// Returns:
//   - bool: true if URL was added and should be crawled, false if skipped
func (s *SiteMap) appendURL(url *url.URL) bool {
	// We shouldn't crawl if the url is not valid or is in an external domain
	if !s.domainValidator.Validate(s.url, url) {
		return false
	}

	for _, v := range s.urlValidators {
		if !v.Validate(url) {
			return false
		}
	}

	urlString := url.String()
	urlString = strings.TrimRight(urlString, "/")

	// We could always lock over a normal mutex, but by using a RWMutex
	// we should increase the throughput of checking duplicate urls.
	// It's reasonable to expect many duplicates on a typical page (in the
	// navigation bar for example), so it's a reasonable to expect that many
	// calls to shouldCrawl will not yield write contention.
	s.rwl.RLock()
	maybeCrawl := !s.siteURLS[urlString]
	s.rwl.RUnlock()

	if !maybeCrawl {
		return false
	}

	// Even when we do write to update the urls list, we could have lost out
	// in a race condition, so reading again is necessary after acquiring the
	// write lock.
	s.rwl.Lock()
	crawl := !s.siteURLS[urlString]
	if crawl {
		s.siteURLS[urlString] = true
		s.sitemapURLS[urlString] = &sitemap.URL{
			Loc:        urlString,
			ChangeFreq: sitemap.Daily,
			Priority:   s.priority.Get(url),
		}
	}
	s.rwl.Unlock()
	return crawl
}

// updatedMod updates the last-modified time for a URL in the sitemap.
//
// This method is called when a page is crawled to record when it was last
// accessed or when a last-modified time is extracted from the page content.
//
// Parameters:
//   - url: URL to update
//   - extractedTime: Last-modified time extracted from page content (nil for current time)
//
// Returns:
//   - bool: true if the URL was found and updated, false if URL not in sitemap
func (s *SiteMap) updatedMod(url *url.URL, extractedTime *time.Time) bool {
	urlString := strings.TrimRight(url.String(), "/")
	s.rwl.Lock()
	defer s.rwl.Unlock()
	val := s.sitemapURLS[urlString]
	if val == nil {
		return false
	}
	if extractedTime != nil {
		val.LastMod = extractedTime
		return true
	}

	if val.LastMod == nil {
		t := time.Now()
		val.LastMod = &t
		return true
	}

	return false
}

// WriteMap writes all discovered URLs to the provided writer, one per line.
//
// URLs are sorted alphabetically for consistent output. This method is
// useful for debugging or generating a simple text list of crawled URLs.
//
// Note: This outputs plain text URLs, not XML format. Use the sitemap
// package's WriteTo method for XML output.
//
// Parameters:
//   - out: Writer to output URLs to
func (s *SiteMap) WriteMap(out io.Writer) {
	s.rwl.RLock()
	defer s.rwl.RUnlock()

	paths := make([]string, 0, len(s.siteURLS))
	for u := range s.siteURLS {
		paths = append(paths, u)
	}
	sort.Strings(paths)

	for _, path := range paths {
		io.WriteString(out, path)
		io.WriteString(out, "\n")
	}
}

// GetURLS returns a copy of all sitemap URL metadata.
//
// The returned map is a snapshot; modifications to it will not affect
// the internal state. This method is thread-safe.
//
// Returns:
//   - map[string]*sitemap.URL: Snapshot of URL strings to their metadata
func (s *SiteMap) GetURLS() map[string]*sitemap.URL {
	s.rwl.RLock()
	defer s.rwl.RUnlock()
	copy := make(map[string]*sitemap.URL, len(s.sitemapURLS))
	for k, v := range s.sitemapURLS {
		copy[k] = v
	}
	return copy
}

// LinkReader is an iterative parser that extracts all href links from an HTML page.
//
// The LinkReader:
//  1. Fetches the page content via HTTP
//  2. Parses HTML using a streaming tokenizer (memory efficient)
//  3. Extracts href attributes from anchor (<a>) tags
//  4. Returns links one at a time via the Read() method
//
// The reader also optionally extracts last-modified times from page content
// when configured via EventCallbackReadLink.
//
// Usage:
//
//	reader := NewLinkReader(url, client, config)
//	for {
//	    link, err := reader.Read()
//	    if err == io.EOF {
//	        break
//	    }
//	    // Process link...
//	}
type LinkReader struct {
	client      *http.Client    // HTTP client for fetching page content
	pageURL     *url.URL        // URL of the page being read
	content     []byte          // Cached page content (loaded on first Read)
	doc         *html.Tokenizer // HTML tokenizer for streaming parsing
	lastModTime *time.Time      // Extracted last-modified time (optional)
	config      *Config         // Crawler configuration
	done        bool            // Flag indicating all links have been read
}

// NewLinkReader creates a new LinkReader for the specified URL.
//
// The LinkReader does not fetch the page content immediately. The HTTP
// request is deferred until the first call to Read().
//
// Parameters:
//   - pageURL: URL of the page to parse
//   - client: HTTP client for fetching content
//   - config: Crawler configuration (used for callback functions)
//
// Returns:
//   - *LinkReader: Initialized reader ready to extract links
func NewLinkReader(pageURL *url.URL, client *http.Client, config *Config) *LinkReader {
	return &LinkReader{
		client:  client,
		pageURL: pageURL,
		config:  config,
	}
}

// Read returns the next href link found in the HTML document.
//
// This method:
//  1. Fetches the page content on first call (if not already cached)
//  2. Handles HTTP redirects by returning the redirect location
//  3. Uses streaming HTML parsing for memory efficiency
//  4. Returns io.EOF when all links have been read
//
// Special cases:
//   - HTTP redirects (3xx): Returns the redirect location and sets done=true
//   - Parse errors: Returns the error (usually io.EOF at end of document)
//
// Returns:
//   - string: The href attribute value of the next link
//   - error: io.EOF when no more links, or error if parsing failed
func (u *LinkReader) Read() (string, error) {
	if u.done {
		return "", io.EOF
	}

	if u.content == nil {
		// If the response is a redirect we should read the location header
		// It is valid for 201 to return a location header but this should
		// not happen as a response to http GET
		resp, respErr := u.client.Get(u.pageURL.String())
		if respErr != nil {
			return "", fmt.Errorf("http get error: %q", respErr)
		}
		if resp.StatusCode >= 300 && resp.StatusCode <= 399 {
			if err := resp.Body.Close(); err != nil {
				return "", err
			}
			locationURL, err := resp.Location()
			if err != nil {
				return "", err
			}
			u.done = true
			return locationURL.String(), nil
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		u.content = body
	}

	if u.doc == nil {
		u.doc = html.NewTokenizer(io.NopCloser(bytes.NewReader(u.content)))
	}

	// Read the href attributes from all a tags using a streaming tokenizer
	for {
		tt := u.doc.Next()
		switch tt {
		case html.ErrorToken:
			return "", u.doc.Err()
		case html.StartTagToken:
			tn, hasAttr := u.doc.TagName()
			if len(tn) == 1 && tn[0] == 'a' && hasAttr {

				// Read the href attribute from the link
				for {
					key, val, moreAttr := u.doc.TagAttr()
					if bytes.Equal(key, hrefAttr) {
						return string(val), nil
					}
					if !moreAttr {
						break
					}
				}
			}
		}
	}
}

// URL returns the URL string of the page being read.
//
// Returns:
//   - string: The page URL as a string
func (u *LinkReader) URL() string {
	return u.pageURL.String()
}

// GetBody returns the raw HTML content of the page.
//
// This is useful for extracting additional metadata from the page content,
// such as last-modified times embedded in the HTML.
//
// Returns:
//   - []byte: Raw HTML content (nil if page not yet fetched)
func (u *LinkReader) GetBody() []byte {
	return u.content
}

// SetLastModTime sets the extracted last-modified time for the page.
//
// This method is typically called by the EventCallbackReadLink callback
// after parsing the page content for timestamp information.
//
// Parameters:
//   - t: Last-modified time to store
func (u *LinkReader) SetLastModTime(t *time.Time) {
	u.lastModTime = t
}
