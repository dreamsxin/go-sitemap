package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dreamsxin/go-sitemap"
	"github.com/dreamsxin/go-sitemap/crawl"
	"go.uber.org/zap"
)

const concurrency int = 8
const crawlTimeout time.Duration = 0
const timeout time.Duration = 30 * time.Second
const keepAlive time.Duration = crawl.DefaultKeepAlive
const interval time.Duration = 48 * time.Hour

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

	flag.Parse()

	if *urlPtr == "" {
		flag.Usage()
		os.Exit(1)
	}

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
	siteMap, siteMapErr := crawl.CrawlDomain(
		*urlPtr,
		crawl.SetMaxConcurrency(*concPtr),
		crawl.SetCrawlTimeout(*crawlTimeoutPtr),
		crawl.SetKeepAlive(*keepAlivePtr),
		crawl.SetTimeout(*timeoutPtr),
		crawl.SetClient(client),
		crawl.SetLogger(logger),
		crawl.SetSitemapURLS(oldurls),
		crawl.SetCrawlValidator(func(pageURL *url.URL) bool {
			urlString := strings.TrimRight(pageURL.String(), "/")
			v, ok := oldurls[urlString]
			if ok {
				if v.Priority == 1 {
					return true
				}
				if v.LastMod != nil {
					sub := nowtime.Sub(*v.LastMod)
					if sub < *intervalPtr {
						logger.Debug("validate url failed (url is not changed)",
							zap.String("url", urlString),
						)
						return false
					}
				}
			}
			return true
		}),
	)

	if siteMapErr != nil {
		log.Fatalf("error: %s", siteMapErr)
	}
	//siteMap.WriteMap(os.Stdout)

	file, err := os.OpenFile(*outfile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.ModePerm)
	if err != nil {
		log.Fatalf("error: %s", err)
	}
	defer file.Close()

	// 初始化
	sm := sitemap.New()

	urls := siteMap.GetURLS()
	for _, v := range urls {
		sm.Add(v)
	}

	//排序
	sort.Slice(sm.URLs, func(i, j int) bool {
		return sm.URLs[i].Priority >= sm.URLs[j].Priority
	})

	if *xmlheader != "" {
		sm.SkipWriteHeader = true
		file.WriteString(*xmlheader) //`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<?xml-stylesheet type="text/xsl" href="sitemap.xsl"?>` + "\n"
	}
	sm.WriteTo(file)
}

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
