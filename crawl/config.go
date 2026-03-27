// Package crawl provides web crawling functionality to automatically discover
// and catalog all URLs within a website domain.
//
// The crawler respects robots.txt conventions, handles concurrent requests
// efficiently, and generates sitemap-compatible URL structures with priority
// and last-modification metadata.
package crawl

import (
	"fmt"
	"net/http"
	"time"

	"github.com/dreamsxin/go-sitemap"
	"go.uber.org/zap"
)

// DefaultMaxConcurrency is the default number of concurrent goroutines used
// for crawling pages. This value also configures the HTTP client's connection
// pool to ensure sufficient connections for parallel requests.
const DefaultMaxConcurrency = 8

// DefaultMaxPendingURLS limits the maximum size of the pending URL queue.
// This prevents memory exhaustion in cases where URLs are dynamically generated
// or contain session-specific parameters that create infinite URL spaces.
const DefaultMaxPendingURLS = 8192

// DefaultCrawlTimeout is the default maximum duration for the entire crawl operation.
// A value of 0 means no timeout (crawl runs until completion).
const DefaultCrawlTimeout = time.Duration(0)

// DefaultTimeout is the default HTTP request timeout for individual page requests.
const DefaultTimeout = time.Second * 10

// DefaultKeepAlive is the default keep-alive timeout for HTTP client connections.
const DefaultKeepAlive = time.Second * 30

// Config holds all configuration options for the web crawler.
//
// Use NewConfig() to create a Config with sensible defaults, then customize
// using Option functions (SetMaxConcurrency, SetTimeout, etc.).
//
// Fields:
//   - MaxConcurrency: Number of parallel goroutines for crawling
//   - MaxPendingURLS: Maximum queue size for pending URLs
//   - CrawlTimeout: Maximum total duration for crawl operation (0 = no limit)
//   - KeepAlive: HTTP connection keep-alive timeout
//   - Timeout: HTTP request timeout per page
//   - Client: Custom HTTP client (optional, created automatically if nil)
//   - Logger: Zap logger for debug/info/warning messages
//   - CrawlValidator: Custom validator for determining if a URL should be crawled
//   - UrlValidators: Additional validators for filtering URLs
//   - DomainValidator: Validates that discovered URLs belong to the target domain
//   - EventCallbackReadLink: Callback function invoked when a new link is discovered
//   - Priority: Strategy for assigning priority values to URLs
//   - SitemapURLS: Pre-populated URLs from existing sitemap (for incremental crawls)
type Config struct {
	MaxConcurrency        int
	MaxPendingURLS        int
	CrawlTimeout          time.Duration
	KeepAlive             time.Duration
	Timeout               time.Duration
	Client                *http.Client
	Logger                *zap.Logger
	CrawlValidator        CrawlValidator
	UrlValidators         []UrlValidator
	DomainValidator       DomainValidator
	EventCallbackReadLink EventCallbackReadLink
	Priority              Priority
	SitemapURLS           map[string]*sitemap.URL
}

// NewConfig creates a new Config instance with sensible defaults and applies
// any provided Option functions to customize the configuration.
//
// Default values:
//   - MaxConcurrency: 8 parallel goroutines
//   - MaxPendingURLS: 8192 URL queue limit
//   - CrawlTimeout: No timeout (0)
//   - Timeout: 10 seconds per HTTP request
//   - KeepAlive: 30 seconds for connection reuse
//
// If no HTTP client is provided via SetClient(), a default client is created
// with connection pooling configured for the specified concurrency level.
// Redirects are automatically disabled to prevent crawling outside the target domain.
//
// Example:
//
//	config := crawl.NewConfig(
//	    crawl.SetMaxConcurrency(16),
//	    crawl.SetTimeout(30*time.Second),
//	    crawl.SetLogger(customLogger),
//	)
func NewConfig(options ...Option) *Config {
	config := &Config{
		MaxConcurrency:  DefaultMaxConcurrency,
		MaxPendingURLS:  DefaultMaxPendingURLS,
		CrawlTimeout:    DefaultCrawlTimeout,
		KeepAlive:       DefaultKeepAlive,
		Timeout:         DefaultTimeout,
		Client:          nil,
		Logger:          nil,
		CrawlValidator:  nil,
		DomainValidator: nil,
	}

	// Options are applied first to inform client options if none is set
	for _, opt := range options {
		opt.apply(config)
	}

	if config.Client == nil {
		config.Client = &http.Client{
			// Following redirects is disabled by default because they
			// could redirect outside the current domain
			CheckRedirect: overrideRedirect,
			Transport: &http.Transport{
				MaxIdleConns:        config.MaxConcurrency,
				MaxIdleConnsPerHost: config.MaxConcurrency,
				MaxConnsPerHost:     config.MaxConcurrency,
				IdleConnTimeout:     config.KeepAlive,
			},
			Timeout: config.Timeout,
		}
	}

	if config.Logger == nil {
		logger, loggerErr := zap.NewProduction(zap.IncreaseLevel(zap.WarnLevel))
		if loggerErr != nil {
			logger = zap.NewNop()
		}
		config.Logger = logger
	}

	if config.DomainValidator == nil {
		config.DomainValidator = DomainValidatorFunc(ValidateHosts)
	}

	if config.Priority == nil {
		config.Priority = PriorityFunc(GetPriority)
	}

	return config
}

// Validate checks that all required configuration fields have valid values.
//
// This method is called automatically by NewDomainCrawler() but can also be
// called manually to verify configuration before use.
//
// Validation rules:
//   - MaxConcurrency must be > 0
//   - MaxPendingURLS must be > 0
//   - KeepAlive must be >= 0
//   - Timeout must be >= 0
//   - Client must be non-nil
//   - Logger must be non-nil
//   - DomainValidator must be non-nil
//
// Returns:
//   - error: Description of the first validation failure, or nil if all checks pass
func (config *Config) Validate() error {
	if config.MaxConcurrency <= 0 {
		return fmt.Errorf("config.MaxConcurrency must be greater than 0")
	}

	if config.MaxPendingURLS <= 0 {
		return fmt.Errorf("config.MaxPendingURLS must be greater than 0")
	}

	if config.KeepAlive < time.Duration(0) {
		return fmt.Errorf("config.KeepAlive duration should be >= 0s")
	}

	if config.Timeout < time.Duration(0) {
		return fmt.Errorf("config.Timeout duration should be >= 0s")
	}

	if config.Client == nil {
		return fmt.Errorf("config.Client must be defined")
	}

	if config.Logger == nil {
		return fmt.Errorf("config.Logger must be defined")
	}

	if config.DomainValidator == nil {
		return fmt.Errorf("config.DomainValidator must be defined")
	}

	return nil
}

// Option is the interface for configuration option functions.
//
// Options provide a fluent, type-safe way to customize Config fields.
// Use the provided Set* functions (SetMaxConcurrency, SetTimeout, etc.)
// to create Option instances and pass them to NewConfig().
type Option interface {
	apply(config *Config)
}

// optionFunc is a function adapter that implements the Option interface.
// This allows ordinary functions to be used as configuration options.
type optionFunc func(config *Config)

// apply invokes the option function with the provided config.
// This method satisfies the Option interface.
func (o optionFunc) apply(config *Config) {
	o(config)
}

// SetSitemapURLS pre-populates the crawler with existing sitemap URLs.
//
// This is useful for incremental crawls where you want to preserve existing
// URL metadata (like LastMod times) and only update changed pages.
//
// Parameters:
//   - urls: Map of URL strings to sitemap.URL objects from a previous crawl
func SetSitemapURLS(urls map[string]*sitemap.URL) Option {
	return optionFunc(func(config *Config) {
		config.SitemapURLS = urls
	})
}

// SetMaxConcurrency sets the number of parallel goroutines for crawling.
//
// Higher values increase crawling speed but also increase memory usage and
// server load. The HTTP client's connection pool is automatically configured
// to support the specified concurrency level.
//
// Recommended values:
//   - Small sites (< 1000 pages): 4-8
//   - Medium sites (1000-10000 pages): 8-16
//   - Large sites (> 10000 pages): 16-32
//
// Parameters:
//   - maxConcurrency: Number of concurrent crawling goroutines (must be > 0)
func SetMaxConcurrency(maxConcurrency int) Option {
	return optionFunc(func(config *Config) {
		config.MaxConcurrency = maxConcurrency
	})
}

// SetMaxPendingURLS sets the maximum size of the pending URL queue.
//
// This limit prevents memory exhaustion in edge cases where URLs are
// dynamically generated (e.g., session IDs, tracking parameters) creating
// an effectively infinite URL space.
//
// When the queue is full, newly discovered URLs are logged and discarded.
//
// Parameters:
//   - maxPendingURLS: Maximum queue size (must be > 0)
func SetMaxPendingURLS(maxPendingURLS int) Option {
	return optionFunc(func(config *Config) {
		config.MaxPendingURLS = maxPendingURLS
	})
}

// SetCrawlTimeout sets the maximum duration for the entire crawl operation.
//
// When the timeout expires, crawling stops and a partial sitemap is returned
// with all URLs discovered up to that point. This is useful for:
//   - Preventing runaway crawls on very large sites
//   - Enforcing time limits in scheduled jobs
//   - Testing crawl behavior with time constraints
//
// Parameters:
//   - crawlTimeout: Maximum crawl duration (0 or negative = no timeout)
func SetCrawlTimeout(crawlTimeout time.Duration) Option {
	return optionFunc(func(config *Config) {
		config.CrawlTimeout = crawlTimeout
	})
}

// SetKeepAlive sets the HTTP connection keep-alive timeout.
//
// Longer keep-alive times improve performance by reusing connections,
// but consume server resources. Shorter times free resources faster
// but may increase connection overhead.
//
// Parameters:
//   - keepAlive: Connection keep-alive duration (>= 0)
func SetKeepAlive(keepAlive time.Duration) Option {
	return optionFunc(func(config *Config) {
		config.KeepAlive = keepAlive
	})
}

// SetTimeout sets the HTTP request timeout for individual page requests.
//
// This timeout applies to each page fetch operation. If a page takes longer
// than this duration to respond, the request is aborted and the page is skipped.
//
// Parameters:
//   - timeout: Per-request timeout duration (>= 0)
func SetTimeout(timeout time.Duration) Option {
	return optionFunc(func(config *Config) {
		config.Timeout = timeout
	})
}

// SetClient provides a custom HTTP client for crawling operations.
//
// Use this when you need:
//   - Custom TLS configuration (e.g., custom CA certificates)
//   - Proxy settings
//   - Custom authentication
//   - Specialized transport configurations
//
// Note: When a custom client is provided, SetKeepAlive and SetTimeout are ignored.
// The client's own timeout and keep-alive settings take precedence.
//
// Important: The client is cloned internally to prevent redirect following
// (which could lead to crawling outside the target domain). The original
// client is not modified.
//
// Parameters:
//   - client: Custom HTTP client (nil to use default)
func SetClient(client *http.Client) Option {
	return optionFunc(func(config *Config) {
		if client == nil {
			config.Client = nil
			return
		}

		// If a custom client is used, we need to make sure that redirects
		// are not followed without mutating the original client
		var overrideClient = *client
		overrideClient.CheckRedirect = overrideRedirect
		config.Client = &overrideClient
	})
}

// SetLogger provides a custom Zap logger for crawl operations.
//
// The default logger outputs warnings and errors to stderr. Use this function
// to customize logging behavior, such as:
//   - Enabling debug-level logging
//   - Writing logs to a file
//   - Using structured JSON logging
//   - Integrating with existing logging infrastructure
//
// Parameters:
//   - logger: Custom Zap logger instance
func SetLogger(logger *zap.Logger) Option {
	return optionFunc(func(config *Config) {
		config.Logger = logger
	})
}

// SetCrawlValidator sets a custom validator function to determine if a URL
// should be crawled based on its sitemap.URL metadata.
//
// The validator is called for each URL before crawling. Return true to crawl,
// false to skip. This is useful for:
//   - Skipping recently updated pages (using LastMod)
//   - Prioritizing high-priority pages
//   - Implementing custom crawl logic
//
// Parameters:
//   - validator: Function that returns true if the URL should be crawled
func SetCrawlValidator(validator CrawlValidator) Option {
	return optionFunc(func(config *Config) {
		config.CrawlValidator = validator
	})
}

// SetDomainValidator overrides the default domain validation function.
//
// The domain validator ensures that discovered links belong to the same
// domain as the root URL, preventing the crawler from following links to
// external sites.
//
// The default validator (ValidateHosts) compares only the host component
// of URLs, ignoring scheme (http/https) and performing no DNS lookups.
//
// Use this function to implement custom domain validation logic, such as:
//   - Allowing subdomains
//   - Validating against a whitelist of domains
//   - Performing DNS lookups for additional security
//
// Parameters:
//   - validator: DomainValidator implementation
func SetDomainValidator(validator DomainValidator) Option {
	return optionFunc(func(config *Config) {
		config.DomainValidator = validator
	})
}

// SetUrlValidator adds a URL validator to the crawler's validation chain.
//
// URL validators are called for each discovered link to determine if it
// should be included in the crawl. Multiple validators can be added and
// all must return true for the URL to be crawled.
//
// Common use cases:
//   - Skipping URLs with query parameters
//   - Skipping URLs with fragments
//   - Filtering by URL pattern or path
//
// Parameters:
//   - validator: UrlValidator implementation to add
func SetUrlValidator(validator UrlValidator) Option {
	return optionFunc(func(config *Config) {
		config.UrlValidators = append(config.UrlValidators, validator)
	})
}

// SetPriority sets a custom priority calculation strategy for URLs.
//
// The priority function assigns a value between 0.0 and 1.0 to each URL,
// indicating its relative importance. This value is included in the
// generated sitemap to help search engines prioritize crawling.
//
// The default strategy (GetPriority) assigns priority based on URL depth:
//   - Root page (/): 1.0
//   - First level (/*): 0.8
//   - Second level (/*/*): 0.6
//   - Deeper pages: 0.4
//
// Parameters:
//   - priority: Priority implementation
func SetPriority(priority Priority) Option {
	return optionFunc(func(config *Config) {
		config.Priority = priority
	})
}

// overrideRedirect prevents the HTTP client from following redirects.
//
// This is critical for sitemap crawling because redirects could lead to
// external domains outside the scope of the sitemap. When a redirect is
// encountered, the crawler uses the original response instead of following
// the redirect location.
func overrideRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

// SetEventCallbackReadLink sets a callback function that is invoked whenever
// a new link is discovered during crawling.
//
// The callback receives:
//   - pageURL: The resolved URL of the discovered link
//   - linkReader: The LinkReader instance that found the link (provides access to page content)
//
// Use cases:
//   - Logging discovered URLs in real-time
//   - Extracting additional metadata from page content
//   - Implementing custom link processing logic
//   - Extracting last-modified times from page content
//
// Parameters:
//   - callback: Function to invoke for each discovered link
func SetEventCallbackReadLink(callback EventCallbackReadLink) Option {
	return optionFunc(func(config *Config) {
		config.EventCallbackReadLink = callback
	})
}
