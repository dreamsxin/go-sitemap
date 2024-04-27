package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/dreamsxin/go-sitemap"
	"github.com/dreamsxin/go-sitemap/crawl"
	"go.uber.org/zap"
)

const concurrency int = 8
const crawlTimeout time.Duration = 0
const timeout time.Duration = 30 * time.Second
const keepAlive time.Duration = crawl.DefaultKeepAlive

func main() {
	urlPtr := flag.String("u", "", "url to crawl (required)")
	concPtr := flag.Int("c", concurrency, "maximum concurrency")
	crawlTimeoutPtr := flag.Duration("w", crawlTimeout, "maximum crawl time")
	timeoutPtr := flag.Duration("t", timeout, "http request timeout")
	keepAlivePtr := flag.Duration("k", keepAlive, "http keep alive timeout")
	verbosePtr := flag.Bool("v", false, "enable verbose logging")
	debugPtr := flag.Bool("d", false, "enable debug logs")
	outfile := flag.String("o", "sitemap.xml", "output file name")
	xmlheader := flag.String("h", "", "xml header")

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

	siteMap, siteMapErr := crawl.CrawlDomain(
		*urlPtr,
		crawl.SetMaxConcurrency(*concPtr),
		crawl.SetCrawlTimeout(*crawlTimeoutPtr),
		crawl.SetKeepAlive(*keepAlivePtr),
		crawl.SetTimeout(*timeoutPtr),
		crawl.SetClient(client),
		crawl.SetLogger(logger),
	)

	if siteMapErr != nil {
		log.Fatalf("error: %s", siteMapErr)
	}
	//siteMap.WriteMap(os.Stdout)

	if *outfile == "" {
		log.Fatal("output file name cant't empay")
	}
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
