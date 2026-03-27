// Command crawl is a CLI tool for automatically generating XML sitemaps
// by crawling a website and discovering all accessible URLs.
//
// Usage:
//
//	crawl -u https://example.com -o sitemap.xml
//	crawl -u https://example.com -o sitemap.xml -c 16 -t 30s
//	crawl -u https://example.com -o sitemap.xml -p priority.json -skip-query=true
//
// Features:
//   - Concurrent crawling with configurable parallelism
//   - Automatic priority calculation based on URL depth
//   - Last-modified time extraction from page content
//   - Incremental crawling (skips recently updated pages)
//   - Query string and fragment filtering options
package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dreamsxin/go-sitemap"
	"github.com/dreamsxin/go-sitemap/crawl"
	"go.uber.org/zap"
)

// Default configuration constants
const (
	concurrency  int           = 8                      // Default number of concurrent crawlers
	crawlTimeout time.Duration = 0                      // Default crawl timeout (0 = no limit)
	timeout      time.Duration = 30 * time.Second       // Default HTTP request timeout
	keepAlive    time.Duration = crawl.DefaultKeepAlive // Default connection keep-alive
	interval     time.Duration = 24 * time.Hour         // Default interval for considering pages "changed"
)

// Settings holds per-path configuration for extracting last-modified times
// from page content using regex patterns.
type Settings struct {
	LastModRegex  string `json:"lastmod-regex"`  // Regex pattern to match timestamp in HTML
	LastModFormat string `json:"lastmod-format"` // Time format for parsing (empty = RFC3339)
}

// main is the entry point for the crawl command.
//
// It parses command-line flags, loads configuration files (priority, settings),
// initializes the crawler with appropriate options, and writes the generated
// sitemap to the specified output file.
func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	urlPtr := flag.String("u", "", "url to crawl (required)")
	concPtr := flag.Int("c", concurrency, "maximum concurrency")
	crawlTimeoutPtr := flag.Duration("w", crawlTimeout, "maximum crawl time")
	timeoutPtr := flag.Duration("t", timeout, "http request timeout")
	keepAlivePtr := flag.Duration("k", keepAlive, "http keep alive timeout")
	verbosePtr := flag.Bool("v", false, "enable verbose logging")
	debugPtr := flag.Bool("d", false, "enable debug logs")
	outfile := flag.String("o", "sitemap.xml", "output file name")
	xmlheader := flag.String("h", "", "xml header")
	intervalPtr := flag.Duration("i", interval, "change frequency interval")
	priorityfile := flag.String("p", "", "priority file")
	settingfile := flag.String("s", "", "setting file")
	// 忽略带有 query 的 url
	skipquery := flag.Bool("skip-query", false, "skip query")
	skipfragment := flag.Bool("skip-fragment", false, "skip fragment")

	flag.Parse()

	if *urlPtr == "" {
		flag.Usage()
		os.Exit(1)
	}

	/*
	   Priority configuration file format (JSON):

	   {
	       "default": {
	           "default": 0.4
	       },
	       "noquery": {
	           "0": 1.0,
	           "1": 0.9,
	           "2": 0.8,
	           "3": 0.6,
	           "4": 0.4
	       },
	       "hasquery": {
	           "0": 0.7,
	           "1": 0.7,
	           "2": 0.4,
	           "3": 0.2,
	           "4": 0.1
	       }
	   }

	   Keys:
	   - "default": Fallback priority values
	   - "noquery": Priorities for URLs without query strings (key = path depth)
	   - "hasquery": Priorities for URLs with query strings or fragments
	*/
	priorityMap := map[string]map[string]float32{}
	if *priorityfile != "" {
		b, err := os.ReadFile(*priorityfile)
		if err != nil {
			log.Fatalf("read priority file error: %s", err)
		}
		json.Unmarshal(b, &priorityMap)
	}

	// Load settings for extracting last-modified times from page content
	settings := map[string]Settings{}
	if *settingfile != "" {
		b, err := os.ReadFile(*settingfile)
		if err != nil {
			log.Fatalf("read setting file error: %s", err)
		}
		json.Unmarshal(b, &settings)
	}

	// Create HTTP client with custom TLS config (skip certificate verification)
	// and connection pooling configured for the specified concurrency level
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:        *concPtr,
			MaxIdleConnsPerHost: *concPtr,
			MaxConnsPerHost:     *concPtr,
			IdleConnTimeout:     *keepAlivePtr,
		},
		Timeout: *timeoutPtr,
	}

	logger, loggerErr := newLogger(*verbosePtr, *debugPtr)
	if loggerErr != nil {
		log.Fatalf("error: %s", loggerErr)
	}

	if *outfile == "" {
		log.Fatal("output file name cant't empay")
	}

	// Load existing sitemap URLs for incremental crawling
	// This preserves last-modified times and allows skipping recently updated pages
	oldurls := make(map[string]*sitemap.URL)
	if *intervalPtr > 0 {
		// 读取文件内容
		file, err := os.OpenFile(*outfile, os.O_RDONLY, os.ModePerm)
		if err != nil {
			log.Printf("error: %s\n", err)
		} else {
			defer file.Close()
			sm := sitemap.New()
			sm.ReadFrom(file)

			for _, v := range sm.URLs {
				oldurls[v.Loc] = v
			}
		}
	}

	nowtime := time.Now()

	// Build crawler configuration options
	options := []crawl.Option{
		crawl.SetMaxConcurrency(*concPtr),
		crawl.SetCrawlTimeout(*crawlTimeoutPtr),
		crawl.SetKeepAlive(*keepAlivePtr),
		crawl.SetTimeout(*timeoutPtr),
		crawl.SetClient(client),
		crawl.SetLogger(logger),
		crawl.SetSitemapURLS(oldurls),
		// Crawl validator: skip URLs that haven't changed since last crawl
		crawl.SetCrawlValidator(func(v *sitemap.URL) bool {
			if v != nil {
				if v.Priority == 1 {
					return true
				}
				if v.LastMod != nil {
					sub := nowtime.Sub(*v.LastMod)
					if sub < *intervalPtr {
						logger.Debug("validate url failed (url is not changed)",
							zap.String("url", v.Loc),
						)
						return false
					}
				}
			}
			return true
		}),
		// Event callback: extract last-modified time from page content
		crawl.SetEventCallbackReadLink(func(hrefResolved *url.URL, linkReader *crawl.LinkReader) {
			if strings.Contains(hrefResolved.Path, "/404") {
				logger.Debug("Read",
					zap.String("page", hrefResolved.String()),
					zap.String("link", linkReader.URL()),
				)
			}

			if len(settings) > 0 {
				for m, setting := range settings {
					if strings.Contains(hrefResolved.Path, m) {
						logger.Debug("extractLastModTime", zap.String("url", linkReader.URL()), zap.Any("regex", setting.LastModRegex))

						regex, err := regexp.Compile(setting.LastModRegex)
						if err != nil {
							logger.Error("extractLastModTime", zap.Error(err))
							return
						}
						matches := regex.FindSubmatch(linkReader.GetBody())
						if len(matches) < 2 {
							logger.Debug("extractLastModTime", zap.String("url", linkReader.URL()), zap.Any("regex", setting.LastModRegex), zap.Any("msg", "not match"),
								zap.Any("matches", matches),
							)
							return
						}

						timeStr := string(matches[1])

						var t time.Time
						if setting.LastModFormat != "" {
							t, err = time.Parse(setting.LastModFormat, timeStr)
						} else {
							t, err = time.Parse(time.RFC3339, timeStr)
						}

						if err != nil {
							logger.Error("extractLastModTime", zap.String("url", linkReader.URL()),
								zap.Error(err),
							)
							return
						}
						linkReader.SetlastModTime(&t)
						logger.Debug("extractLastModTime", zap.String("url", linkReader.URL()),
							zap.Any("time", t.String()),
						)
						return
					}
				}
			}

		}),
	}

	// Add URL validator to skip URLs with query parameters if requested
	if *skipquery {
		options = append(options, crawl.SetUrlValidator(crawl.UrlValidatorFunc(func(link *url.URL) bool {
			return link.RawQuery == ""
		})))
	}

	// Add URL validator to skip URLs with fragments if requested
	if *skipfragment {
		options = append(options, crawl.SetUrlValidator(crawl.UrlValidatorFunc(func(link *url.URL) bool {
			return link.Fragment == ""
		})))
	}

	// Configure custom priority calculation if priority map is provided
	if len(priorityMap) > 0 {
		options = append(options, crawl.SetPriority(crawl.PriorityFunc(func(link *url.URL) float32 {
			if link == nil {
				return 0.0
			}
			num := "0"
			path := strings.Trim(link.Path, "/")
			if path != "" {
				parts := strings.Split(path, "/")
				num = fmt.Sprintf("%d", len(parts))
			}
			// Assign priority based on URL type (query, fragment, or clean)
			if link.RawQuery != "" {
				if v, ok := priorityMap["hasquery"][num]; ok {
					return v
				}
			} else if link.Fragment != "" {
				if v, ok := priorityMap["hasfragment"][num]; ok {
					return v
				}
				if v, ok := priorityMap["hasquery"][num]; ok {
					return v
				}
			} else {
				if v, ok := priorityMap["noquery"][num]; ok {
					return v
				}
			}

			// 默认值 - Default fallback value
			if v, ok := priorityMap["default"]["default"]; ok {
				return v
			}

			return 0.4
		})))
	}

	// Execute the crawl
	siteMap, siteMapErr := crawl.CrawlDomain(
		*urlPtr,
		options...,
	)

	if siteMapErr != nil {
		log.Fatalf("error: %s", siteMapErr)
	}
	//siteMap.WriteMap(os.Stdout)

	// Write the generated sitemap to output file
	file, err := os.OpenFile(*outfile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.ModePerm)
	if err != nil {
		log.Fatalf("error: %s", err)
	}
	defer file.Close()

	// 初始化 - Initialize new sitemap
	sm := sitemap.New()

	urls := siteMap.GetURLS()
	for _, v := range urls {
		sm.Add(v)
	}

	// 排序 - Sort URLs by priority (highest first)
	sort.Slice(sm.URLs, func(i, j int) bool {
		return sm.URLs[i].Priority >= sm.URLs[j].Priority
	})

	// Write custom XML header if provided
	if *xmlheader != "" {
		sm.SkipWriteHeader = true
		file.WriteString(*xmlheader) //`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<?xml-stylesheet type="text/xsl" href="sitemap.xsl"?>` + "\n"
	}
	sm.WriteTo(file)
}

// newLogger creates a Zap logger configured for the specified verbosity level.
//
// Parameters:
//   - verbose: If true, enables info-level logging
//   - debug: If true, enables debug-level logging (overrides verbose)
//
// Returns:
//   - *zap.Logger: Configured logger instance
//   - error: Error if logger creation fails
//
// Logger behavior:
//   - Neither verbose nor debug: Returns a no-op logger (no output)
//   - Verbose only: Info level and above
//   - Debug: Debug level and above (most verbose)
func newLogger(verbose bool, debug bool) (*zap.Logger, error) {
	if !verbose && !debug {
		return zap.NewNop(), nil
	}

	config := zap.Config{
		Level:       zap.NewAtomicLevelAt(zap.InfoLevel),
		Development: false,
		Sampling: &zap.SamplingConfig{
			Initial:    100,
			Thereafter: 100,
		},
		Encoding:         "json",
		EncoderConfig:    zap.NewProductionEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if debug {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	return config.Build()
}
