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

const (
	defaultAPIURL      = "http://data.zz.baidu.com/urls"
	defaultBatchSize   = 50
	defaultSitemapPath = "./sitemap.xml"
)

// Config holds all application configuration parameters
type Config struct {
	APIURL      string
	SiteDomain  string
	Token       string
	SitemapPath string
	BatchSize   int
	CheckUpdate bool
	DaysOffset  int
}

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

// parseFlags processes command line arguments and returns configuration
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

// loadAndParseSitemap reads and parses the sitemap file
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

// filterURLsByUpdateTime filters URLs based on last modification time
func filterURLsByUpdateTime(urls []*sitemap.URL, config *Config) []string {
	if !config.CheckUpdate || config.DaysOffset <= 0 {
		return extractURLs(urls)
	}

	cutoff := time.Now().AddDate(0, 0, -config.DaysOffset)
	var filtered []string

	for _, url := range urls {
		if url.LastMod.After(cutoff) {
			filtered = append(filtered, url.Loc)
		}
	}

	log.Printf("Filtered to %d URLs updated in the last %d days", len(filtered), config.DaysOffset)
	return filtered
}

// extractURLs extracts loc values from sitemap URLs
func extractURLs(urls []*sitemap.URL) []string {
	result := make([]string, len(urls))
	for i, url := range urls {
		result[i] = url.Loc
	}
	return result
}

// submitURLsInBatches sends URLs to Baidu in batches
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

// submitBatchToBaidu sends a single batch of URLs to Baidu API
func submitBatchToBaidu(urls []string, config *Config) error {
	client := &http.Client{Timeout: 10 * time.Second}
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
