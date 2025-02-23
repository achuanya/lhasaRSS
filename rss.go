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
)

// RSSProcessor 用来抓取并处理 RSS
type RSSProcessor struct {
	config     *Config
	httpClient *http.Client
	cosClient  *COSClientWrapper // 封装后的 COS 客户端
	parser     *gofeed.Parser
	avatarMap  map[string]string
}

// COSClientWrapper 这里简单包一下，可扩展
type COSClientWrapper struct {
	inner *http.Client
}

func NewRSSProcessor(config *Config) *RSSProcessor {
	// 自定义 Transport 用于连接池和复用
	transport := &http.Transport{
		MaxIdleConns:        config.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     config.MaxConcurrency,
		MaxIdleConnsPerHost: config.MaxConcurrency,
	}

	// 创建 http.Client
	httpClient := &http.Client{
		Timeout:   config.HTTPTimeout,
		Transport: transport,
	}

	// 初始化 COS 客户端
	cosClient := initCOSClient(config)

	return &RSSProcessor{
		config:     config,
		httpClient: httpClient,
		cosClient:  &COSClientWrapper{inner: cosClient.Client},
		parser:     gofeed.NewParser(),
		avatarMap:  make(map[string]string),
	}
}

func (p *RSSProcessor) Close() {
	// 释放 httpClient 的空闲连接
	p.httpClient.CloseIdleConnections()
}

// Run 运行整个抓取流程
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

	// 并发抓取所有 RSS
	articles, errs := p.fetchAllRSS(ctx, feeds)
	if len(errs) > 0 {
		// 合并并打印错误，但不退出
		for _, e := range errs {
			LogError(e)
		}
	}

	// 保存数据到 COS
	if err := p.saveToCOS(ctx, articles); err != nil {
		return fmt.Errorf("保存到 COS 失败: %w", err)
	}

	return nil
}

// loadAvatars 从 COS 上加载 avatar_data.json
func (p *RSSProcessor) loadAvatars(ctx context.Context) error {
	content, err := p.fetchCOSFile(ctx, "data/avatar_data.json")
	if err != nil {
		return fmt.Errorf("读取头像数据失败: %w", err)
	}

	var avatarData []AvatarData
	if err := json.Unmarshal([]byte(content), &avatarData); err != nil {
		return fmt.Errorf("解析头像数据失败: %w", err)
	}

	for _, a := range avatarData {
		domain, err := extractDomain(a.DomainName)
		if err != nil {
			// 不退出，记录错误即可
			LogError(fmt.Errorf("头像域名解析失败: %w", err))
			continue
		}
		p.avatarMap[domain] = a.Avatar
	}
	return nil
}

// getFeeds 从 COS 上读取 rss_feeds.txt 文件得到订阅列表
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

// fetchAllRSS 并发处理所有 RSS
func (p *RSSProcessor) fetchAllRSS(ctx context.Context, feeds []string) ([]Article, []error) {
	// 为每个 feed 创建一个 goroutine，使用工作池
	var (
		articles []Article
		errs     []error
		mutex    sync.Mutex
	)

	feedChan := make(chan string, len(feeds))
	wg := sync.WaitGroup{}

	// 启动工作池
	for i := 0; i < p.config.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feedURL := range feedChan {
				// 为单个请求设置一个超时上下文，避免卡住
				reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				article, err := p.processFeed(reqCtx, feedURL)
				cancel()

				mutex.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					articles = append(articles, article)
				}
				mutex.Unlock()
			}
		}()
	}

	// 分发任务
	for _, feed := range feeds {
		feedChan <- feed
	}
	close(feedChan)

	// 等待所有 worker 完成
	wg.Wait()
	return articles, errs
}

// processFeed 抓取并解析单个 RSS，取最新一篇文章
func (p *RSSProcessor) processFeed(ctx context.Context, feedURL string) (Article, error) {
	// 1) 获取并清理内容
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
		return Article{}, fmt.Errorf("获取源失败 (%s): %w", feedURL, err)
	}

	// 解析 RSS
	parsedFeed, err := withRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (*gofeed.Feed, error) {
		return p.parser.ParseString(body)
	})
	if err != nil {
		return Article{}, fmt.Errorf("解析源失败 (%s): %w", feedURL, err)
	}

	// 域名 or 头像处理
	domain, err := extractDomain(parsedFeed.Link)
	if err != nil {
		// 域名解析失败，暂用 unknown
		domain = "unknown"
	}

	avatarURL := p.avatarMap[domain]
	if avatarURL == "" {
		avatarURL = "https://cos.lhasa.icu/LinksAvatar/default.png"
	}

	// 取最新一篇文章
	if len(parsedFeed.Items) == 0 {
		return Article{}, fmt.Errorf("没有找到任何文章 (%s)", feedURL)
	}
	item := parsedFeed.Items[0]

	// 尝试解析发布时间，失败则 updated
	publishedTime, err := parseTime(item.Published)
	if err != nil && item.Updated != "" {
		publishedTime, err = parseTime(item.Updated)
	}
	if err != nil {
		return Article{}, fmt.Errorf("时间解析失败 (%s)", feedURL)
	}

	// 标题过长，手动映射
	name := mapNameIfNeeded(parsedFeed.Title)

	return Article{
		DomainName: domain,
		Name:       name,
		Title:      item.Title,
		Link:       item.Link,
		Date:       formatTime(publishedTime),
		Avatar:     avatarURL,
	}, nil
}

// mapNameIfNeeded 如果 feed.Title 在映射表中，则返回简短名称
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

// extractDomain 提取域名（带 scheme）
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
