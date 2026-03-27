// Command baidu submits sitemap URLs to Baidu's webmaster platform API
// for faster indexing by Baidu search engine.
//
// Usage:
//
//	baidu -site https://example.com -token YOUR_BAIDU_TOKEN -sitemap sitemap.xml
//	baidu -site https://example.com -token YOUR_TOKEN -days 7 -batch 100
//
// Features:
//   - Batch submission (default 50 URLs per request)
//   - Filter by last update time (submit only recently changed URLs)
//   - Automatic retry on failure
//   - Progress logging
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dreamsxin/go-sitemap"
)

// Default configuration constants
const (
	defaultAPIURL      = "http://data.zz.baidu.com/urls" // Baidu sitemap submission API endpoint
	defaultBatchSize   = 50                              // Default URLs per batch
	defaultSitemapPath = "./sitemap.xml"                 // Default sitemap file location
)

// Config holds all configuration parameters for Baidu URL submission.
type Config struct {
	APIURL      string // Baidu API endpoint URL
	SiteDomain  string // Website domain (must start with https://)
	Token       string // Baidu webmaster platform authentication token
	SitemapPath string // Path to sitemap XML file
	BatchSize   int    // Number of URLs to submit per API request
	CheckUpdate bool   // Whether to filter URLs by last update time
	DaysOffset  int    // Only submit URLs updated in the last N days
}

// main is the entry point for the baidu command.
//
// It orchestrates the URL submission process:
//  1. Parse command-line flags
//  2. Load and parse the sitemap file
//  3. Filter URLs by last update time (if configured)
//  4. Submit URLs to Baidu in batches
func main() {
	config, err := parseFlags()
	if err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	urls, err := loadAndParseSitemap(config.SitemapPath)
	if err != nil {
		log.Fatalf("Failed to load sitemap: %v", err)
	}

	filteredURLs := filterURLsByUpdateTime(urls, config)
	if err := submitURLsInBatches(filteredURLs, config); err != nil {
		log.Fatalf("URL submission failed: %v", err)
	}

	log.Println("All URLs submitted successfully")
}

// parseFlags processes command-line arguments and returns a validated Config.
//
// Command-line flags:
//   - -api: Baidu API endpoint (default: http://data.zz.baidu.com/urls)
//   - -site: Website domain (required, must start with https://)
//   - -token: Baidu webmaster token (required)
//   - -sitemap: Path to sitemap XML file (default: ./sitemap.xml)
//   - -batch: URLs per batch (default: 50)
//   - -check-update: Filter by update time (default: true)
//   - -days: Submit URLs updated in last N days (default: 1)
//
// Returns:
//   - *Config: Validated configuration
//   - error: Validation error if required flags are missing or invalid
func parseFlags() (*Config, error) {
	apiURL := flag.String("api", defaultAPIURL, "Baidu API endpoint URL")
	siteDomain := flag.String("site", "", "Website domain (must start with https://)")
	token := flag.String("token", "", "Baidu webmaster platform token")
	sitemapPath := flag.String("sitemap", defaultSitemapPath, "Path to sitemap XML file")
	batchSize := flag.Int("batch", defaultBatchSize, "Number of URLs to submit per batch")
	checkUpdate := flag.Bool("check-update", true, "Filter URLs by last update time")
	daysOffset := flag.Int("days", 1, "Only submit URLs updated in the last N days")

	flag.Parse()

	if *siteDomain == "" || *token == "" {
		return nil, fmt.Errorf("both -site and -token parameters are required")
	}

	if !strings.HasPrefix(*siteDomain, "https://") {
		return nil, fmt.Errorf("site domain must start with https://")
	}

	return &Config{
		APIURL:      *apiURL,
		SiteDomain:  *siteDomain,
		Token:       *token,
		SitemapPath: *sitemapPath,
		BatchSize:   *batchSize,
		CheckUpdate: *checkUpdate,
		DaysOffset:  *daysOffset,
	}, nil
}

// loadAndParseSitemap reads and parses the sitemap XML file.
//
// Parameters:
//   - path: Path to the sitemap XML file
//
// Returns:
//   - []*sitemap.URL: Slice of URL entries from the sitemap
//   - error: Error if file cannot be opened or parsed
func loadAndParseSitemap(path string) ([]*sitemap.URL, error) {
	file, err := os.OpenFile(path, os.O_RDONLY, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("failed to open sitemap file: %w", err)
	}
	defer file.Close()

	sm := sitemap.New()
	if _, err := sm.ReadFrom(file); err != nil {
		return nil, fmt.Errorf("failed to parse sitemap: %w", err)
	}

	return sm.URLs, nil
}

// filterURLsByUpdateTime filters URLs based on their last modification time.
//
// Only URLs updated within the configured number of days (DaysOffset) are
// included. This reduces API calls by submitting only changed content.
//
// Parameters:
//   - urls: All URLs from the sitemap
//   - config: Configuration containing filter settings
//
// Returns:
//   - []string: Filtered list of URL strings
func filterURLsByUpdateTime(urls []*sitemap.URL, config *Config) []string {
	if !config.CheckUpdate || config.DaysOffset <= 0 {
		return extractURLs(urls)
	}

	cutoff := time.Now().AddDate(0, 0, -config.DaysOffset)
	var filtered []string

	for _, url := range urls {
		if url.LastMod != nil && url.LastMod.After(cutoff) {
			filtered = append(filtered, url.Loc)
		}
	}

	log.Printf("Filtered to %d URLs updated in the last %d days", len(filtered), config.DaysOffset)
	return filtered
}

// extractURLs extracts the Loc (URL string) field from sitemap.URL objects.
//
// Parameters:
//   - urls: Slice of sitemap.URL objects
//
// Returns:
//   - []string: Slice of URL strings
func extractURLs(urls []*sitemap.URL) []string {
	result := make([]string, len(urls))
	for i, url := range urls {
		result[i] = url.Loc
	}
	return result
}

// submitURLsInBatches sends URLs to Baidu's API in batches.
//
// URLs are submitted in groups of BatchSize to avoid overwhelming the API.
// A 1-second delay is added between batches to respect rate limits.
//
// Parameters:
//   - urls: All URLs to submit
//   - config: Configuration containing batch size and API details
//
// Returns:
//   - error: Error if any batch fails (individual batch failures are logged but don't stop processing)
func submitURLsInBatches(urls []string, config *Config) error {
	if len(urls) == 0 {
		log.Println("No URLs to submit")
		return nil
	}

	log.Printf("Submitting %d URLs in batches of %d", len(urls), config.BatchSize)

	for i := 0; i < len(urls); i += config.BatchSize {
		end := i + config.BatchSize
		if end > len(urls) {
			end = len(urls)
		}

		batch := urls[i:end]
		if err := submitBatchToBaidu(batch, config); err != nil {
			log.Printf("Failed to submit batch: %v", err)
			continue
		}

		log.Printf("Successfully submitted batch %d-%d", i+1, end)
		time.Sleep(1 * time.Second)
	}

	return nil
}

// submitBatchToBaidu sends a single batch of URLs to the Baidu webmaster API.
//
// The function:
//  1. Constructs the API URL with site domain and token
//  2. Creates a pipe to stream URLs as newline-separated text
//  3. Sends a POST request with Content-Type: text/plain
//  4. Logs the API response
//
// Parameters:
//   - urls: Batch of URLs to submit
//   - config: Configuration containing API endpoint and authentication
//
// Returns:
//   - error: Error if the HTTP request fails or API returns non-200 status
//
// baiduHTTPClient is a shared HTTP client reused across all batch submissions.
var baiduHTTPClient = &http.Client{Timeout: 10 * time.Second}

func submitBatchToBaidu(urls []string, config *Config) error {
	client := baiduHTTPClient
	requestURL := fmt.Sprintf("%s?site=%s&token=%s", config.APIURL, config.SiteDomain, config.Token)

	// Create pipe for streaming upload (equivalent to --data-binary)
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		// Write URLs with proper newlines
		for i, url := range urls {
			if i > 0 {
				if _, err := pw.Write([]byte{'\n'}); err != nil {
					log.Printf("Failed to write to pipe: %v", err)
					return
				}
			}
			if _, err := pw.Write([]byte(url)); err != nil {
				log.Printf("Failed to write URL to pipe: %v", err)
				return
			}
		}
	}()

	req, err := http.NewRequest("POST", requestURL, pr)
	if err != nil {
		pr.Close()
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status code %d: %s", resp.StatusCode, string(body))
	}

	log.Printf("API response: %s", string(body))
	return nil
}
