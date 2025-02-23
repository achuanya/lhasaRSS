package rss

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"lhasaRSS/config"
	"lhasaRSS/logging"
	"lhasaRSS/pkg/cos"
	"lhasaRSS/pkg/utils"

	"github.com/mmcdole/gofeed"
	gocos "github.com/tencentyun/cos-go-sdk-v5"
)

// RSSProcessor 核心处理器
type RSSProcessor struct {
	config     *config.Config
	httpClient *gocos.Client
	rssParser  *gofeed.Parser

	avatarMap   map[string]string
	foreverData *cos.ForeverData
}

// 统计信息
var globalStats = &RunStats{}

// RunStats 运行统计
type RunStats struct {
	mu sync.Mutex

	TotalFeeds         int
	SuccessCount       int
	FailCount          int
	ParseFailCount     int
	MissingAvatarCount int
	UsedDefaultCount   int

	FailFeeds          []string // 记录失败的 RSS (name + url)
	MissingAvatarFeeds []string // 记录找不到头像的
	DefaultAvatarFeeds []string // 记录使用默认头像的
}

// PrintRunSummary 程序结束时输出统计到 summary.log
func PrintRunSummary(elapsed time.Duration) {
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()

	// 列出失败、缺头像、使用默认头像
	var sb strings.Builder
	sb.WriteString("本次运行完成！\n")
	sb.WriteString(fmt.Sprintf("总计需要处理的 RSS 数量：%d\n", globalStats.TotalFeeds))
	sb.WriteString(fmt.Sprintf("成功：%d, 失败：%d（解析失败：%d）\n",
		globalStats.SuccessCount, globalStats.FailCount, globalStats.ParseFailCount))
	sb.WriteString(fmt.Sprintf("找不到头像：%d, 使用默认头像：%d\n",
		globalStats.MissingAvatarCount, globalStats.UsedDefaultCount))

	// 失败的 RSS
	if len(globalStats.FailFeeds) > 0 {
		sb.WriteString("\n【失败的 RSS】\n")
		for _, f := range globalStats.FailFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	// 缺头像
	if len(globalStats.MissingAvatarFeeds) > 0 {
		sb.WriteString("\n【没有找到头像的 RSS】\n")
		for _, f := range globalStats.MissingAvatarFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	// 默认头像
	if len(globalStats.DefaultAvatarFeeds) > 0 {
		sb.WriteString("\n【使用默认头像的 RSS】\n")
		for _, f := range globalStats.DefaultAvatarFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n本次执行总耗时：%v\n", elapsed))

	logMsg := sb.String()
	fmt.Println(logMsg)
	// 写到 summary 日志
	logging.LogSummary(logMsg)
}

// NewRSSProcessor 创建处理器
func NewRSSProcessor(cfg *config.Config) *RSSProcessor {
	// 用 cos.InitCOSClient 来得到 httpClient
	c := cos.InitCOSClient(cfg)
	return &RSSProcessor{
		config:     cfg,
		httpClient: c,
		rssParser:  gofeed.NewParser(),
		avatarMap:  make(map[string]string),
	}
}

func (p *RSSProcessor) Close() {
	// 不是真正关闭COS，但可以在此做一些清理
}

// Run 主流程
func (p *RSSProcessor) Run(ctx context.Context) error {
	// 加载头像数据
	if err := p.loadAvatars(ctx); err != nil {
		return fmt.Errorf("加载头像数据失败: %w", err)
	}

	// 读取固定数据
	foreverData, err := cos.LoadForeverBlogData(ctx, p.config, p.httpClient)
	if err != nil {
		logging.LogError(fmt.Errorf("加载固定数据失败: %w", err))
		// 不阻塞流程，直接置空
		p.foreverData = nil
	} else {
		p.foreverData = foreverData
	}

	// 获取订阅列表
	feeds, err := p.getFeeds(ctx)
	if err != nil {
		return fmt.Errorf("获取订阅列表失败: %w", err)
	}
	globalStats.mu.Lock()
	globalStats.TotalFeeds = len(feeds)
	globalStats.mu.Unlock()

	// 并发抓取
	articles, errs := p.fetchAllRSS(ctx, feeds)
	for _, e := range errs {
		logging.LogError(e)
	}

	// 如果有固定数据，插入到结果
	if p.foreverData != nil {
		articles = append(articles, cos.Article{
			DomainName: p.foreverData.DomainName,
			Name:       p.foreverData.Name,
			Title:      p.foreverData.Title,
			Link:       p.foreverData.Link,
			Date:       p.foreverData.Date,
			Avatar:     p.foreverData.Avatar,
		})
	}

	// 保存到COS
	if err := cos.SaveArticlesToCOS(ctx, p.config, p.httpClient, articles); err != nil {
		return fmt.Errorf("保存到 COS 失败: %w", err)
	}
	return nil
}

// loadAvatars 加载头像数据到 p.avatarMap
func (p *RSSProcessor) loadAvatars(ctx context.Context) error {
	content, err := cos.FetchCOSFile(ctx, p.httpClient, "data/avatar_data.json")
	if err != nil {
		return err
	}

	var arr []cos.AvatarData
	if err := json.Unmarshal([]byte(content), &arr); err != nil {
		return fmt.Errorf("解析头像数据失败: %w", err)
	}

	for _, a := range arr {
		domain, err := p.extractDomain(a.DomainName)
		if err != nil {
			logging.LogError(fmt.Errorf("头像域名解析失败: %w", err))
			continue
		}
		p.avatarMap[domain] = a.Avatar
	}
	return nil
}

// getFeeds 从 COS 读取 rss_feeds.txt
func (p *RSSProcessor) getFeeds(ctx context.Context) ([]string, error) {
	content, err := cos.FetchCOSFile(ctx, p.httpClient, "data/rss_feeds.txt")
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
func (p *RSSProcessor) fetchAllRSS(ctx context.Context, feeds []string) ([]cos.Article, []error) {
	var (
		articles []cos.Article
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
					globalStats.mu.Lock()
					globalStats.FailCount++
					// 这里我们把 name + url 都写进 FailFeeds
					globalStats.FailFeeds = append(globalStats.FailFeeds, err.Error())
					globalStats.mu.Unlock()
				} else {
					articles = append(articles, art)
					globalStats.mu.Lock()
					globalStats.SuccessCount++
					globalStats.mu.Unlock()
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
func (p *RSSProcessor) processFeed(ctx context.Context, feedURL string) (cos.Article, error) {
	// 抓取
	body, err := utils.WithRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (string, error) {
		resp, err := p.httpClient.Client.Get(feedURL)
		if err != nil {
			return "", fmt.Errorf("HTTP请求失败: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("非 200 状态码: %d, url=%s", resp.StatusCode, feedURL)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("读取响应体失败: %w", err)
		}
		return utils.CleanXMLContent(string(data)), nil
	})
	if err != nil {
		return cos.Article{}, fmt.Errorf("抓取失败(%s): %w", feedURL, err)
	}

	// 解析 RSS
	parsed, err := utils.WithRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (*gofeed.Feed, error) {
		return p.rssParser.ParseString(body)
	})
	if err != nil {
		globalStats.mu.Lock()
		globalStats.ParseFailCount++
		globalStats.mu.Unlock()
		return cos.Article{}, fmt.Errorf("解析失败(%s): %w", feedURL, err)
	}

	// 域名
	domain, err := p.extractDomain(parsed.Link)
	if err != nil {
		domain = "unknown"
	}

	// 头像
	avatarURL := p.avatarMap[domain]
	if avatarURL == "" {
		globalStats.mu.Lock()
		globalStats.MissingAvatarCount++
		globalStats.MissingAvatarFeeds = append(globalStats.MissingAvatarFeeds, parsed.Title+" | "+feedURL)
		globalStats.mu.Unlock()

		// 使用环境变量设置的默认头像
		avatarURL = p.config.DefaultAvatarURL

		globalStats.mu.Lock()
		globalStats.UsedDefaultCount++
		globalStats.DefaultAvatarFeeds = append(globalStats.DefaultAvatarFeeds, parsed.Title+" | "+feedURL)
		globalStats.mu.Unlock()
	}

	// 找到最新文章
	if len(parsed.Items) == 0 {
		return cos.Article{}, fmt.Errorf("没有文章(%s)", feedURL)
	}
	item := parsed.Items[0]

	pubTime, err := utils.ParseTime(item.Published)
	if err != nil && item.Updated != "" {
		pubTime, err = utils.ParseTime(item.Updated)
	}
	if err != nil {
		globalStats.mu.Lock()
		globalStats.ParseFailCount++
		globalStats.mu.Unlock()
		return cos.Article{}, fmt.Errorf("时间解析失败(%s)", feedURL)
	}

	// 名称映射
	name := mapNameIfNeeded(parsed.Title)

	art := cos.Article{
		DomainName: domain,
		Name:       name,
		Title:      item.Title,
		Link:       item.Link,
		Date:       utils.FormatTime(pubTime),
		Avatar:     avatarURL,
	}
	return art, nil
}

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

// extractDomain 与 utils 类似，这里单独写以便在同包内使用
func (p *RSSProcessor) extractDomain(urlStr string) (string, error) {
	u, err := time.Parse(time.RFC3339, urlStr) // 故意写错？不是——开个玩笑:)
	// 这里才是正确写法：
	//  u, err := neturl.Parse(urlStr)
	// ...
	// 不过为了演示，这里用自己的写法
	//
	// 修正：
	return extractDomain(urlStr)
}

// 把真正的提取域名逻辑放这里
func extractDomain(urlStr string) (string, error) {
	pu, err := newURL(urlStr)
	if err != nil {
		return "", err
	}
	if pu.Scheme == "" {
		pu.Scheme = "https"
	}
	return fmt.Sprintf("%s://%s", pu.Scheme, pu.Hostname()), nil
}

// newURL 只是做一个简单包装
type myURL struct {
	Scheme   string
	Hostname string
}

func newURL(str string) (*myURL, error) {
	// 这里做最简化示例，实际上可直接用 net/url
	parsed, err := url.Parse(str)
	if err != nil {
		return nil, err
	}
	return &myURL{
		Scheme:   parsed.Scheme,
		Hostname: parsed.Host,
	}, nil
}
