package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tencentyun/cos-go-sdk-v5"
)

// RSSProcessor 抓取处理 RSS
type RSSProcessor struct {
	config     *Config
	httpClient *http.Client
	cosClient  *cos.Client
	parser     *gofeed.Parser

	// 头像映射
	avatarMap map[string]string
}

// 全局的统计信息
var stats = &RunStats{}

// RunStats 爬虫状态统计
type RunStats struct {
	mu                   sync.Mutex
	TotalFeeds           int // 总共要处理的 RSS 数量
	SuccessCount         int // 成功解析到最新文章的 RSS
	FailCount            int // 抓取或解析失败的 RSS
	ParseFailCount       int // 时间/解析失败
	MissingAvatarCount   int // 未能在 avatarMap 中找到头像
	UsedDefaultAvatarCnt int // 使用了默认头像
}

// PrintRunSummary 程序执行完后，打印统计信息到 summary.log
func PrintRunSummary(elapsed time.Duration) {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	message := fmt.Sprintf(
		"本次运行完成！\n"+
			"总共需要处理的 RSS 数量：%d\n"+
			"成功：%d, 失败：%d\n"+
			"时间/解析失败：%d\n"+
			"找不到头像：%d, 使用默认头像：%d\n"+
			"总耗时：%v\n",
		stats.TotalFeeds,
		stats.SuccessCount,
		stats.FailCount,
		stats.ParseFailCount,
		stats.MissingAvatarCount,
		stats.UsedDefaultAvatarCnt,
		elapsed,
	)

	fmt.Println(message)
	// 写入 summary.log
	LogSummary(message)
}

// NewRSSProcessor 构造函数
func NewRSSProcessor(config *Config) *RSSProcessor {
	// httpClient
	transport := &http.Transport{
		MaxIdleConns:        config.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     config.MaxConcurrency,
		MaxIdleConnsPerHost: config.MaxConcurrency,
	}
	httpClient := &http.Client{
		Timeout:   config.HTTPTimeout,
		Transport: transport,
	}
	// cosClient
	cosClient := initCOSClient(config)

	return &RSSProcessor{
		config:     config,
		httpClient: httpClient,
		cosClient:  cosClient,
		parser:     gofeed.NewParser(),
		avatarMap:  make(map[string]string),
	}
}

func (p *RSSProcessor) Close() {
	p.httpClient.CloseIdleConnections()
}

func (p *RSSProcessor) Run(ctx context.Context) error {
	// 加载头像数据
	if err := p.loadAvatars(ctx); err != nil {
		return fmt.Errorf("加载头像数据失败: %w", err)
	}

	// 获取订阅列表
	feeds, err := p.getFeeds(ctx)
	if err != nil {
		return fmt.Errorf("获取订阅列表失败: %w", err)
	}

	stats.mu.Lock()
	stats.TotalFeeds = len(feeds)
	stats.mu.Unlock()

	// 并发抓取所有 RSS
	articles, errs := p.fetchAllRSS(ctx, feeds)
	for _, e := range errs {
		LogError(e)
	}

	// 保存数据到 COS
	if err := p.saveToCOS(ctx, articles); err != nil {
		return fmt.Errorf("保存到 COS 失败: %w", err)
	}
	return nil
}

// loadAvatars 从 COS 获取 avatar_data.json
func (p *RSSProcessor) loadAvatars(ctx context.Context) error {
	content, err := p.fetchCOSFile(ctx, "data/avatar_data.json")
	if err != nil {
		return err
	}

	var avatarData []AvatarData
	if err := json.Unmarshal([]byte(content), &avatarData); err != nil {
		return fmt.Errorf("解析头像数据失败: %w", err)
	}

	for _, a := range avatarData {
		domain, err := extractDomain(a.DomainName)
		if err != nil {
			LogError(fmt.Errorf("头像域名解析失败: %w", err))
			continue
		}
		p.avatarMap[domain] = a.Avatar
	}
	return nil
}

// getFeeds 从 COS 获取 rss_feeds.txt
func (p *RSSProcessor) getFeeds(ctx context.Context) ([]string, error) {
	content, err := p.fetchCOSFile(ctx, "data/rss_feeds.txt")
	if err != nil {
		return nil, err
	}
	var feeds []string
	scanner := bufio.NewScanner(bytes.NewReader([]byte(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			feeds = append(feeds, line)
		}
	}
	return feeds, nil
}

// fetchAllRSS 并发抓取
func (p *RSSProcessor) fetchAllRSS(ctx context.Context, feeds []string) ([]Article, []error) {
	var (
		articles []Article
		errs     []error
		mutex    sync.Mutex
	)
	feedChan := make(chan string, len(feeds))
	wg := sync.WaitGroup{}

	for i := 0; i < p.config.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feedURL := range feedChan {
				reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				art, err := p.processFeed(reqCtx, feedURL)
				cancel()

				mutex.Lock()
				if err != nil {
					errs = append(errs, err)
					stats.mu.Lock()
					stats.FailCount++
					stats.mu.Unlock()
				} else {
					articles = append(articles, art)
					stats.mu.Lock()
					stats.SuccessCount++
					stats.mu.Unlock()
				}
				mutex.Unlock()
			}
		}()
	}

	for _, f := range feeds {
		feedChan <- f
	}
	close(feedChan)
	wg.Wait()
	return articles, errs
}

// processFeed 抓取并解析单个 RSS
func (p *RSSProcessor) processFeed(ctx context.Context, feedURL string) (Article, error) {
	// 抓取
	body, err := withRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (string, error) {
		resp, err := p.httpClient.Get(feedURL)
		if err != nil {
			return "", fmt.Errorf("HTTP 请求失败: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("非 200 状态码: %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("读取响应体失败: %w", err)
		}
		return cleanXMLContent(string(data)), nil
	})
	if err != nil {
		return Article{}, fmt.Errorf("抓取失败 (%s): %w", feedURL, err)
	}

	// 解析
	parsedFeed, err := withRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (*gofeed.Feed, error) {
		return p.parser.ParseString(body)
	})
	if err != nil {
		stats.mu.Lock()
		stats.ParseFailCount++
		stats.mu.Unlock()
		return Article{}, fmt.Errorf("解析失败 (%s): %w", feedURL, err)
	}

	// 域名
	domain, err := extractDomain(parsedFeed.Link)
	if err != nil {
		domain = "unknown"
	}

	// 获取头像
	avatarURL := p.avatarMap[domain]
	if avatarURL == "" {
		stats.mu.Lock()
		stats.MissingAvatarCount++
		stats.mu.Unlock()

		// 使用默认头像
		avatarURL = "https://cos.lhasa.icu/LinksAvatar/default.png"
		stats.mu.Lock()
		stats.UsedDefaultAvatarCnt++
		stats.mu.Unlock()
	}

	// 找到最新文章
	if len(parsedFeed.Items) == 0 {
		// 没有文章
		return Article{}, fmt.Errorf("没有文章 (%s)", feedURL)
	}
	item := parsedFeed.Items[0]

	// 解析发布时间
	pubTime, err := parseTime(item.Published)
	if err != nil && item.Updated != "" {
		pubTime, err = parseTime(item.Updated)
	}
	if err != nil {
		stats.mu.Lock()
		stats.ParseFailCount++
		stats.mu.Unlock()
		return Article{}, fmt.Errorf("时间解析失败 (%s)", feedURL)
	}

	// 名称映射
	name := mapNameIfNeeded(parsedFeed.Title)

	return Article{
		DomainName: domain,
		Name:       name,
		Title:      item.Title,
		Link:       item.Link,
		Date:       formatTime(pubTime),
		Avatar:     avatarURL,
	}, nil
}

// mapNameIfNeeded 标题映射，减少标题字数
func mapNameIfNeeded(title string) string {
	nameMapping := map[string]string{
		"obaby@mars": "obaby",
		"青山小站 | 一个在帝都搬砖的新时代农民工":       "青山小站",
		"Homepage on Miao Yu | 于淼":    "于淼",
		"Homepage on Yihui Xie | 谢益辉": "谢益辉",
	}
	if mapped, ok := nameMapping[title]; ok {
		return mapped
	}
	return title
}

func extractDomain(u string) (string, error) {
	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Hostname()), nil
}
