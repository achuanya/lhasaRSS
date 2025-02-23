package rss

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"lhasaRSS/config"
	"lhasaRSS/logging"
	"lhasaRSS/pkg/cos"
	"lhasaRSS/pkg/utils"

	"github.com/mmcdole/gofeed"
)

/*
RSSProcessor：负责加载各类配置JSON（avatar、feeds、forever_blog、name_mapping），并发抓取RSS，上传结果等
*/
type RSSProcessor struct {
	cfg *config.Config

	// httpClient 用来 GET RSS、以及下载 avatar_data.json 等
	httpClient *http.Client

	// cosClient 用来最终上传 rss_data.json
	cosClient *cos.ArticleCOSClientWrapper // 或者直接 *gocos.Client，也可以

	// RSS 解析器
	parser *gofeed.Parser

	// 头像映射
	avatarMap map[string]string

	// 固定数据
	foreverData []cos.Article

	// 名称映射
	nameMap map[string]string
}

// 全局统计
var globalStats = &RunStats{}

type RunStats struct {
	mu sync.Mutex

	TotalFeeds         int
	SuccessCount       int
	FailCount          int
	ParseFailCount     int
	MissingAvatarCount int
	UsedDefaultCount   int

	FailFeeds          []string // 失败的 RSS
	MissingAvatarFeeds []string // 找不到头像
	DefaultAvatarFeeds []string // 使用默认头像
}

/*
PrintRunSummary：执行完毕后输出统计信息到 summary 日志
*/
func PrintRunSummary(elapsed time.Duration) {
	globalStats.mu.Lock()
	defer globalStats.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("本次运行完成！\n")
	sb.WriteString(fmt.Sprintf("总计需要处理的 RSS 数量：%d\n", globalStats.TotalFeeds))
	sb.WriteString(fmt.Sprintf("成功：%d, 失败：%d（解析失败：%d）\n",
		globalStats.SuccessCount, globalStats.FailCount, globalStats.ParseFailCount))
	sb.WriteString(fmt.Sprintf("找不到头像：%d, 使用默认头像：%d\n",
		globalStats.MissingAvatarCount, globalStats.UsedDefaultCount))

	if len(globalStats.FailFeeds) > 0 {
		sb.WriteString("\n【失败的 RSS】\n")
		for _, f := range globalStats.FailFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	if len(globalStats.MissingAvatarFeeds) > 0 {
		sb.WriteString("\n【没有找到头像的 RSS】\n")
		for _, f := range globalStats.MissingAvatarFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	if len(globalStats.DefaultAvatarFeeds) > 0 {
		sb.WriteString("\n【使用默认头像的 RSS】\n")
		for _, f := range globalStats.DefaultAvatarFeeds {
			sb.WriteString(" - " + f + "\n")
		}
	}

	sb.WriteString(fmt.Sprintf("\n本次执行总耗时：%v\n", elapsed))

	msg := sb.String()
	// 控制台打印
	fmt.Println(msg)
	// 写summary日志
	logging.LogSummary(msg)
}

/*
NewRSSProcessor：构造一个 RSSProcessor
- 创建一个普通 http.Client
- 创建一个 cosClient (可选, 用于上传)
*/
func NewRSSProcessor(cfg *config.Config) *RSSProcessor {
	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     cfg.MaxConcurrency,
		MaxIdleConnsPerHost: cfg.MaxConcurrency,
	}
	httpClient := &http.Client{
		Timeout:   cfg.HTTPTimeout,
		Transport: transport,
	}

	return &RSSProcessor{
		cfg:        cfg,
		httpClient: httpClient,
		// cosClient:  ...  如果你需要封装 cos.Client 也可
		parser:    gofeed.NewParser(),
		avatarMap: make(map[string]string),
		nameMap:   make(map[string]string),
	}
}

func (p *RSSProcessor) Close() {
	p.httpClient.CloseIdleConnections()
}

/*
Run 整体执行流程：
 1. 加载头像数据
 2. 加载固定数据
 3. 加载名称映射
 4. 获取RSS订阅列表
 5. 并发抓取
 6. 插入固定数据
 7. 排序 & 上传到 COS
*/
func (p *RSSProcessor) Run(ctx context.Context) error {
	// 加载头像数据
	if err := p.loadAvatars(ctx); err != nil {
		return fmt.Errorf("加载头像数据失败: %w", err)
	}

	// 加载十年之约
	if err := p.loadForeverData(ctx); err != nil {
		// 如果加载失败，可以记录错误，但不终止流程
		logging.LogError(fmt.Errorf("加载十年之约数据失败: %w", err))
		p.foreverData = nil
	}

	// 加载名称映射
	if err := p.loadNameMapping(ctx); err != nil {
		logging.LogError(fmt.Errorf("加载名称映射失败: %w", err))
	}

	// 获取RSS订阅列表
	feeds, err := p.loadFeeds(ctx)
	if err != nil {
		return fmt.Errorf("获取订阅列表失败: %w", err)
	}
	globalStats.mu.Lock()
	globalStats.TotalFeeds = len(feeds)
	globalStats.mu.Unlock()

	// 并发抓取
	articles, errs := p.fetchAllRSS(ctx, feeds)
	for _, e2 := range errs {
		logging.LogError(e2)
	}

	// 插入十年之约数据
	if len(p.foreverData) > 0 {
		articles = append(articles, p.foreverData...)
	}

	// 排序 (时间倒序)
	sort.Slice(articles, func(i, j int) bool {
		t1, _ := time.Parse("January 2, 2006", articles[i].Date)
		t2, _ := time.Parse("January 2, 2006", articles[j].Date)
		return t1.After(t2)
	})

	// 上传到 COS
	upClient := cos.InitCOSClient(p.cfg)
	if err := cos.SaveArticlesToCOS(ctx, p.cfg, upClient, articles); err != nil {
		return fmt.Errorf("保存到COS失败: %w", err)
	}

	return nil
}

/*
loadAvatars 从 p.cfg.COSAvatar 下载 avatar_data.json
*/
func (p *RSSProcessor) loadAvatars(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSAvatar)
	if err != nil {
		return err
	}
	var arr []cos.AvatarData
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		return fmt.Errorf("解析avatar_data.json失败: %w", err)
	}
	for _, a := range arr {
		dom, err2 := p.extractDomain(a.DomainName)
		if err2 != nil {
			logging.LogError(fmt.Errorf("头像域名解析失败: %w", err2))
			continue
		}
		p.avatarMap[dom] = a.Avatar
	}
	return nil
}

/*
loadForeverData 从 p.cfg.COSForeverBlog 下载 foreverblog.json
本示例假设这个文件里是一个数组，因为你给的示例是 [{...}].
*/
func (p *RSSProcessor) loadForeverData(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSForeverBlog)
	if err != nil {
		return err
	}
	var arr []cos.Article
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		return fmt.Errorf("解析foreverblog.json失败: %w", err)
	}
	p.foreverData = arr
	return nil
}

/*
loadNameMapping 从 p.cfg.COSNameMapping 下载 name_mapping.json
文件示例：[{"longName":"obaby@mars","shortName":"obaby"}, ...]
*/
func (p *RSSProcessor) loadNameMapping(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSNameMapping)
	if err != nil {
		return err
	}
	var data []struct {
		LongName  string `json:"longName"`
		ShortName string `json:"shortName"`
	}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		return fmt.Errorf("解析name_mapping.json失败: %w", err)
	}
	for _, item := range data {
		p.nameMap[item.LongName] = item.ShortName
	}
	return nil
}

/*
loadFeeds 从 p.cfg.COSFavoriteRSS 下载 rss_feeds.txt
*/
func (p *RSSProcessor) loadFeeds(ctx context.Context) ([]string, error) {
	body, err := p.downloadFile(ctx, p.cfg.COSFavoriteRSS)
	if err != nil {
		return nil, err
	}
	var feeds []string
	scanner := bufio.NewScanner(bytes.NewReader([]byte(body)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			feeds = append(feeds, line)
		}
	}
	return feeds, nil
}

/*
fetchAllRSS 并发抓取所有 RSS
*/
func (p *RSSProcessor) fetchAllRSS(ctx context.Context, feeds []string) ([]cos.Article, []error) {
	var (
		articles []cos.Article
		errs     []error
		mutex    sync.Mutex
	)
	ch := make(chan string, len(feeds))
	wg := sync.WaitGroup{}

	for i := 0; i < p.cfg.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feedURL := range ch {
				reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				art, err := p.processFeed(reqCtx, feedURL)
				cancel()

				mutex.Lock()
				if err != nil {
					errs = append(errs, err)
					globalStats.mu.Lock()
					globalStats.FailCount++
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
	for _, feed := range feeds {
		ch <- feed
	}
	close(ch)
	wg.Wait()
	return articles, errs
}

/*
processFeed 抓取并解析单个 RSS，取最新一篇文章
*/
func (p *RSSProcessor) processFeed(ctx context.Context, feedURL string) (cos.Article, error) {
	// 1) GET 原始内容
	body, err := utils.WithRetry(ctx, p.cfg.MaxRetries, p.cfg.RetryInterval, func() (string, error) {
		resp, e2 := p.httpClient.Get(feedURL)
		if e2 != nil {
			return "", fmt.Errorf("HTTP请求失败: %w", e2)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("非200状态: %d, url=%s", resp.StatusCode, feedURL)
		}
		data, e3 := io.ReadAll(resp.Body)
		if e3 != nil {
			return "", fmt.Errorf("读取响应体失败: %w", e3)
		}
		return utils.CleanXMLContent(string(data)), nil
	})
	if err != nil {
		return cos.Article{}, fmt.Errorf("抓取失败(%s): %w", feedURL, err)
	}

	// 2) 解析 RSS
	parsedFeed, err := utils.WithRetry(ctx, p.cfg.MaxRetries, p.cfg.RetryInterval, func() (*gofeed.Feed, error) {
		return p.parser.ParseString(body)
	})
	if err != nil {
		globalStats.mu.Lock()
		globalStats.ParseFailCount++
		globalStats.mu.Unlock()
		return cos.Article{}, fmt.Errorf("解析失败(%s): %w", feedURL, err)
	}

	// 3) 域名
	domain, _ := p.extractDomain(parsedFeed.Link)
	if domain == "" {
		domain = "unknown"
	}

	// 4) 头像
	avatarURL := p.avatarMap[domain]
	if avatarURL == "" {
		globalStats.mu.Lock()
		globalStats.MissingAvatarCount++
		// 记录 "名称 + url"
		globalStats.MissingAvatarFeeds = append(globalStats.MissingAvatarFeeds, parsedFeed.Title+" | "+feedURL)
		globalStats.mu.Unlock()

		// 使用默认头像
		avatarURL = p.cfg.DefaultAvatarURL

		globalStats.mu.Lock()
		globalStats.UsedDefaultCount++
		globalStats.DefaultAvatarFeeds = append(globalStats.DefaultAvatarFeeds, parsedFeed.Title+" | "+feedURL)
		globalStats.mu.Unlock()
	}

	// 5) 最新文章
	if len(parsedFeed.Items) == 0 {
		return cos.Article{}, fmt.Errorf("没有文章(%s)", feedURL)
	}
	item := parsedFeed.Items[0]

	pubTime, e4 := utils.ParseTime(item.Published)
	if e4 != nil && item.Updated != "" {
		pubTime, e4 = utils.ParseTime(item.Updated)
	}
	if e4 != nil {
		globalStats.mu.Lock()
		globalStats.ParseFailCount++
		globalStats.mu.Unlock()
		return cos.Article{}, fmt.Errorf("时间解析失败(%s)", feedURL)
	}

	// 6) 名称映射
	name := p.mapNameIfNeeded(parsedFeed.Title)

	return cos.Article{
		DomainName: domain,
		Name:       name,
		Title:      item.Title,
		Link:       item.Link,
		Date:       utils.FormatTime(pubTime),
		Avatar:     avatarURL,
	}, nil
}

/*
mapNameIfNeeded 从 p.nameMap 查找短名称，如果没有则原样返回
*/
func (p *RSSProcessor) mapNameIfNeeded(longName string) string {
	if short, ok := p.nameMap[longName]; ok {
		return short
	}
	return longName
}

/*
extractDomain 尝试解析一个 URL，提取 scheme://host
*/
func (p *RSSProcessor) extractDomain(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" {
		u.Scheme = "https"
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Hostname()), nil
}

/*
downloadFile 使用 httpClient 下载某个绝对地址
*/
func (p *RSSProcessor) downloadFile(ctx context.Context, fileURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fileURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败(%s): %w", fileURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("下载非200(%s): %d", fileURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取内容失败: %w", err)
	}
	return string(data), nil
}
