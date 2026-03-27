# go-sitemap

[![Go Reference](https://pkg.go.dev/badge/github.com/dreamsxin/go-sitemap.svg)](https://pkg.go.dev/github.com/dreamsxin/go-sitemap)
[![Go Report Card](https://goreportcard.com/badge/github.com/dreamsxin/go-sitemap)](https://goreportcard.com/report/github.com/dreamsxin/go-sitemap)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A comprehensive Go library and CLI toolset for generating XML sitemaps and submitting them to search engines.

## Features

- **XML Sitemap Generation**: Create standards-compliant XML sitemaps following [sitemaps.org](https://www.sitemaps.org/) protocol
- **Automatic Website Crawling**: Discover all URLs within a domain automatically
- **Concurrent Crawling**: High-performance parallel crawling with configurable concurrency
- **Sitemap Indexes**: Support for large sites requiring multiple sitemap files
- **Priority Calculation**: Automatic priority assignment based on URL structure
- **Last-Modified Detection**: Extract and preserve last-modified timestamps
- **Incremental Crawling**: Skip recently updated pages to save time
- **Baidu Submission**: Built-in tool for submitting URLs to Baidu Webmaster Platform
- **Flexible Filtering**: Filter URLs by query parameters, fragments, and custom rules

## Installation

### Install the CLI Tools

```bash
# Install the crawl command
go install github.com/dreamsxin/go-sitemap/cmd/crawl@latest

# Install the Baidu submission command
go install github.com/dreamsxin/go-sitemap/cmd/baidu@latest
```

### Install as a Library

```bash
go get github.com/dreamsxin/go-sitemap
```

## Quick Start

### Generate a Sitemap

```bash
# Basic usage
crawl -u https://example.com -o sitemap.xml

# With custom concurrency and timeout
crawl -u https://example.com -o sitemap.xml -c 16 -t 30s

# With priority configuration
crawl -u https://example.com -o sitemap.xml -p priority.json

# Skip URLs with query parameters
crawl -u https://example.com -o sitemap.xml -skip-query=true

# Skip URLs with fragments
crawl -u https://example.com -o sitemap.xml -skip-fragment=true

# Enable verbose logging
crawl -u https://example.com -o sitemap.xml -v

# Enable debug logging
crawl -u https://example.com -o sitemap.xml -d
```

### Submit to Baidu

**First, get your Baidu token:**

1. Go to [Baidu Webmaster Platform](https://ziyuan.baidu.com/)
2. Add and verify your website
3. Navigate to "Link Submission" → "API Submission"
4. Copy your token

```bash
# Basic submission
baidu -site https://example.com -token YOUR_BAIDU_TOKEN

# Custom batch size and date filter
baidu -site https://example.com -token YOUR_TOKEN -batch 100 -days 7

# Custom sitemap path
baidu -site https://example.com -token YOUR_TOKEN -sitemap ./custom-sitemap.xml

# Submit only URLs from last 7 days
baidu -site https://example.com -token YOUR_TOKEN -days 7
```

## Command-Line Options

### Crawl Command

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-u` | string | (required) | URL to crawl (e.g., `https://example.com`) |
| `-o` | string | `sitemap.xml` | Output file name |
| `-c` | int | `8` | Maximum concurrency (number of parallel crawlers) |
| `-w` | duration | `0` | Maximum crawl time (0 = no limit) |
| `-t` | duration | `30s` | HTTP request timeout per page |
| `-k` | duration | `30s` | HTTP keep-alive timeout |
| `-i` | duration | `48h` | Interval for considering pages "changed" |
| `-p` | string | - | Path to priority configuration JSON file |
| `-s` | string | - | Path to settings JSON file (for last-mod extraction) |
| `-h` | string | - | Custom XML header |
| `-skip-query` | bool | `false` | Skip URLs with query parameters |
| `-skip-fragment` | bool | `false` | Skip URLs with URL fragments |
| `-v` | bool | `false` | Enable verbose logging |
| `-d` | bool | `false` | Enable debug logging |

### Baidu Command

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-site` | string | (required) | Website domain (must start with `https://`) |
| `-token` | string | (required) | Baidu webmaster platform token ([get it here](https://ziyuan.baidu.com/)) |
| `-api` | string | `http://data.zz.baidu.com/urls` | Baidu API endpoint |
| `-sitemap` | string | `./sitemap.xml` | Path to sitemap XML file |
| `-batch` | int | `50` | Number of URLs per submission batch |
| `-check-update` | bool | `true` | Filter URLs by last update time |
| `-days` | int | `1` | Submit only URLs updated in the last N days |

**Getting Your Baidu Token:**

1. Visit [Baidu Webmaster Platform](https://ziyuan.baidu.com/)
2. Register and verify ownership of your website
3. Go to "Link Submission" (链接提交) → "API Submission" (API 提交)
4. Your token will be displayed in the API section

## Configuration Files

### Priority Configuration

Create a `priority.json` file to customize URL priority assignment:

```json
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
```

**Structure:**
- `default.default`: Fallback priority for unmatched URLs
- `noquery`: Priorities for URLs without query strings (key = path depth)
- `hasquery`: Priorities for URLs with query strings or fragments
- `hasfragment`: Priorities for URLs with fragments only (optional)

**Path Depth:**
- `0`: Root page (`/`)
- `1`: First level (`/products`)
- `2`: Second level (`/products/item1`)
- `3+`: Deeper pages

### Settings Configuration (Last-Modified Extraction)

Create a `settings.json` file to extract last-modified times from page content:

```json
{
  "/blog/": {
    "lastmod-regex": "<time[^>]*>([^<]+)</time>",
    "lastmod-format": "2006-01-02"
  },
  "/articles/": {
    "lastmod-regex": "Published: (\\d{4}-\\d{2}-\\d{2})",
    "lastmod-format": "2006-01-02"
  }
}
```

**Fields:**
- `lastmod-regex`: Regular expression to match timestamp in HTML (first capture group is used)
- `lastmod-format`: Go time format string (leave empty for RFC3339)

## Library Usage

### Basic Sitemap Creation

```go
package main

import (
    "os"
    "time"
    "github.com/dreamsxin/go-sitemap"
)

func main() {
    sm := sitemap.New()
    
    t := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
    sm.Add(&sitemap.URL{
        Loc:        "https://example.com/",
        LastMod:    &t,
        ChangeFreq: sitemap.Daily,
        Priority:   1.0,
    })
    
    sm.Add(&sitemap.URL{
        Loc:        "https://example.com/about",
        ChangeFreq: sitemap.Monthly,
        Priority:   0.5,
    })
    
    file, _ := os.Create("sitemap.xml")
    defer file.Close()
    sm.WriteTo(file)
}
```

### Automatic Domain Crawling

```go
package main

import (
    "os"
    "time"
    "github.com/dreamsxin/go-sitemap"
    "github.com/dreamsxin/go-sitemap/crawl"
)

func main() {
    // Crawl domain with custom options
    siteMap, err := crawl.CrawlDomain("https://example.com",
        crawl.SetMaxConcurrency(16),
        crawl.SetTimeout(30*time.Second),
        crawl.SetCrawlTimeout(10*time.Minute),
    )
    if err != nil {
        panic(err)
    }
    
    // Create sitemap from crawled URLs
    sm := sitemap.New()
    for _, url := range siteMap.GetURLS() {
        sm.Add(url)
    }
    
    file, _ := os.Create("sitemap.xml")
    defer file.Close()
    sm.WriteTo(file)
}
```

### Custom URL Filtering

```go
package main

import (
    "net/url"
    "github.com/dreamsxin/go-sitemap/crawl"
)

func main() {
    // Skip URLs with query parameters
    skipQuery := crawl.UrlValidatorFunc(func(link *url.URL) bool {
        return link.RawQuery == ""
    })
    
    // Skip URLs deeper than 3 levels
    maxDepth := crawl.UrlValidatorFunc(func(link *url.URL) bool {
        path := link.Path
        depth := 0
        for _, c := range path {
            if c == '/' {
                depth++
            }
        }
        return depth <= 3
    })
    
    _, err := crawl.CrawlDomain("https://example.com",
        crawl.SetUrlValidator(skipQuery),
        crawl.SetUrlValidator(maxDepth),
    )
}
```

### Sitemap Index for Large Sites

```go
package main

import (
    "os"
    "time"
    "github.com/dreamsxin/go-sitemap"
)

func main() {
    idx := sitemap.NewSitemapIndex()
    
    t := time.Now()
    idx.Add(&sitemap.URL{
        Loc:     "https://example.com/sitemap-posts.xml",
        LastMod: &t,
    })
    idx.Add(&sitemap.URL{
        Loc:     "https://example.com/sitemap-pages.xml",
        LastMod: &t,
    })
    
    file, _ := os.Create("sitemap-index.xml")
    defer file.Close()
    idx.WriteTo(file)
}
```

## Project Structure

```
go-sitemap/
├── sitemap.go              # Core sitemap XML generation
├── sitemapindex.go         # Sitemap index support
├── sitemap_test.go         # Unit tests for sitemap
├── sitemapindex_test.go    # Unit tests for sitemap index
├── crawl/
│   ├── config.go           # Crawler configuration
│   ├── crawl.go            # Core crawling logic
│   └── ...
├── cmd/
│   ├── crawl/              # CLI crawl command
│   │   └── main.go
│   └── baidu/              # CLI Baidu submission command
│       └── main.go
└── README.md
```

## Best Practices

### 1. Concurrency Tuning

- **Small sites** (< 1,000 pages): 4-8 workers
- **Medium sites** (1,000-10,000 pages): 8-16 workers
- **Large sites** (> 10,000 pages): 16-32 workers

Higher concurrency increases speed but also server load.

### 2. Incremental Crawling

Use the interval flag to skip recently crawled pages:

```bash
crawl -u https://example.com -o sitemap.xml -i 48h
```

This skips pages updated within the last 48 hours, saving time and bandwidth.

### 3. URL Filtering

Exclude low-value URLs:

```bash
# Skip tracking parameters
crawl -u https://example.com -skip-query=true

# Skip page fragments
crawl -u https://example.com -skip-fragment=true
```

### 4. Priority Strategy

- Homepage: 1.0
- Category pages: 0.8-0.9
- Product/content pages: 0.6-0.8
- Deep archive pages: 0.3-0.5

## Related Projects

- [go-seo](https://github.com/dreamsxin/go-seo) - SEO toolkit for Go
- [cphalcon7](https://github.com/dreamsxin/cphalcon7) - PHP framework

## Donation

If you find this project useful, consider supporting development:

[Donation Page](https://github.com/dreamsxin/cphalcon7/blob/master/DONATE.md)

## License

MIT License - see [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit issues and pull requests.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request
