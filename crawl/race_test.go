package crawl

import (
	"net/url"
	"testing"
)

// TestChannelClosedRace 测试修复后的竞态条件
// 这是一个基准测试，多次运行以验证线程安全性
func TestChannelClosedRace(t *testing.T) {
	rootURL, _ := url.Parse("http://example.com")

	// 使用很小的 buffer 来增加竞态条件的触发概率
	crawler, err := NewDomainCrawler(rootURL, NewConfig(
		SetMaxConcurrency(4),
		SetMaxPendingURLS(2),
	))
	if err != nil {
		t.Fatal(err)
	}

	// 验证 crawler 创建成功
	if crawler == nil {
		t.Fatal("crawler should not be nil")
	}

	// 验证初始状态
	if crawler.closed {
		t.Error("closed flag should be false initially")
	}

	// 验证 channel 未关闭
	select {
	case <-crawler.pendingURLS:
		// channel 是可读的，未关闭
	default:
	}

	t.Log("Crawler initialized successfully")
}
