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

	"github.com/mmcdole/gofeed"

	"lhasaRSS/config"
	"lhasaRSS/logging"
	"lhasaRSS/pkg/cos"
	"lhasaRSS/pkg/utils"

	gocos "github.com/tencentyun/cos-go-sdk-v5"
)

/*
@author: 游钓四方 <haibao1027@gmail.com>
@description: 核心 - 下载配置/并发抓取RSS/统计/上传COS
*/
type RSSProcessor struct {
	cfg        *config.Config
	httpClient *http.Client   // 用来 GET 各种JSON/TXT、GET RSS
	cosClient  *gocos.Client  // 用来上传最终结果
	parser     *gofeed.Parser // RSS 解析器

	avatarMap   map[string]string // domain->avatar
	foreverData []cos.Article     // 固定数据
	nameMap     map[string]string // longName->shortName
}

type RunStats struct {
	mu sync.Mutex

	TotalFeeds         int
	SuccessCount       int
	FailCount          int
	ParseFailCount     int
	MissingAvatarCount int
	UsedDefaultCount   int

	FailFeeds          []string // 失败RSS (名称+url)
	MissingAvatarFeeds []string // 找不到头像RSS
	DefaultAvatarFeeds []string // 使用默认头像RSS
}

// 全局统计
var globalStats = &RunStats{}

/*
PrintRunSummary：在 main 中调用，输出并记录本次抓取的统计信息
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
	out := sb.String()

	fmt.Println(out)
	logging.LogSummary(out) // summary-YYYY-MM-DD.log
}

/*
NewRSSProcessor：创建一个RSS处理器
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

	// 创建一个 cosClient，用于上传 RSS
	cosClient := cos.InitCOSClient(cfg)

	return &RSSProcessor{
		cfg:        cfg,
		httpClient: httpClient,
		cosClient:  cosClient,
		parser:     gofeed.NewParser(),
		avatarMap:  make(map[string]string),
		nameMap:    make(map[string]string),
	}
}

/*
Close：释放可能需要关闭的资源（这里关闭 httpClient 的空闲连接即可）
*/
func (p *RSSProcessor) Close() {
	p.httpClient.CloseIdleConnections()
}

/*
Run：
  - 获取头像数据
  - 获取固定数据
  - 获取名称映射
  - 并发抓取RSS
  - 与固定数据合并
  - 排序、上传 COS
*/
func (p *RSSProcessor) Run(ctx context.Context) error {
	// 获取头像数据
	if err := p.loadAvatars(ctx); err != nil {
		return fmt.Errorf("加载头像数据失败: %w", err)
	}

	// 获取固定数据
	if err := p.loadForeverData(ctx); err != nil {
		logging.LogError(fmt.Errorf("加载固定数据失败: %w", err))
		p.foreverData = nil
	}

	// 获取名称映射
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
	for _, e := range errs {
		logging.LogError(e)
	}

	// 合并固定数据
	if len(p.foreverData) > 0 {
		articles = append(articles, p.foreverData...)
	}

	// 按时间倒序排序
	sort.Slice(articles, func(i, j int) bool {
		t1, _ := time.Parse("January 2, 2006", articles[i].Date)
		t2, _ := time.Parse("January 2, 2006", articles[j].Date)
		return t1.After(t2)
	})

	// 上传到COS
	if err := p.saveArticlesToCOS(ctx, articles); err != nil {
		return fmt.Errorf("最终保存到COS失败: %w", err)
	}

	return nil
}

/*
loadAvatars：获取 头像数据 并解析到 p.avatarMap
*/
func (p *RSSProcessor) loadAvatars(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSAvatar)
	if err != nil {
		return err
	}
	var arr []cos.AvatarData
	if e2 := json.Unmarshal([]byte(body), &arr); e2 != nil {
		return fmt.Errorf("解析 AvatarData.json 失败: %w", e2)
	}
	for _, a := range arr {
		dom, er2 := p.extractDomain(a.DomainName)
		if er2 != nil {
			logging.LogError(fmt.Errorf("头像域名解析失败: %w", er2))
			continue
		}
		p.avatarMap[dom] = a.Avatar
	}
	return nil
}

/*
loadForeverData：获取 固定数据 并解析到 p.foreverData
*/
func (p *RSSProcessor) loadForeverData(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSForeverBlog)
	if err != nil {
		return err
	}
	var arr []cos.Article
	if e2 := json.Unmarshal([]byte(body), &arr); e2 != nil {
		return fmt.Errorf("解析 foreverblog.json 失败: %w", e2)
	}
	p.foreverData = arr
	return nil
}

/*
loadNameMapping：获取 名称映射 并填充 p.nameMap
*/
func (p *RSSProcessor) loadNameMapping(ctx context.Context) error {
	body, err := p.downloadFile(ctx, p.cfg.COSNameMapping)
	if err != nil {
		return err
	}
	var mArr []struct {
		LongName  string `json:"longName"`
		ShortName string `json:"shortName"`
	}
	if e2 := json.Unmarshal([]byte(body), &mArr); e2 != nil {
		return fmt.Errorf("解析 NameMapping.json 失败: %w", e2)
	}
	for _, item := range mArr {
		p.nameMap[item.LongName] = item.ShortName
	}
	return nil
}

/*
loadFeeds：获取 MyFavoriteRSS.txt 并返回其每行
*/
func (p *RSSProcessor) loadFeeds(ctx context.Context) ([]string, error) {
	body, err := p.downloadFile(ctx, p.cfg.COSFavoriteRSS)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader([]byte(body)))
	var feeds []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			feeds = append(feeds, line)
		}
	}
	return feeds, nil
}

/*
fetchAllRSS：并发抓取所有RSS
*/
func (p *RSSProcessor) fetchAllRSS(ctx context.Context, feeds []string) ([]cos.Article, []error) {
	var (
		articles []cos.Article
		errs     []error
		mutex    sync.Mutex
	)

	taskCh := make(chan string, len(feeds))
	wg := sync.WaitGroup{}

	// 启动多个worker
	for i := 0; i < p.cfg.MaxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feedURL := range taskCh {
				reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				art, e2 := p.processFeed(reqCtx, feedURL)
				cancel()

				mutex.Lock()
				if e2 != nil {
					errs = append(errs, e2)

					globalStats.mu.Lock()
					globalStats.FailCount++
					globalStats.FailFeeds = append(globalStats.FailFeeds, e2.Error())
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

	// 分发feeds
	for _, f := range feeds {
		taskCh <- f
	}
	close(taskCh)
	wg.Wait()

	return articles, errs
}

/*
processFeed：抓取并解析单个 RSS，返回最新文章
*/
func (p *RSSProcessor) processFeed(ctx context.Context, feedURL string) (cos.Article, error) {
	// GET内容,带重试
	body, err := utils.WithRetry(ctx, p.cfg.MaxRetries, p.cfg.RetryInterval, func() (string, error) {
		resp, e2 := p.httpClient.Get(feedURL)
		if e2 != nil {
			return "", fmt.Errorf("HTTP请求失败: %w", e2)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("状态非200(%s): %d", feedURL, resp.StatusCode)
		}
		data, e3 := io.ReadAll(resp.Body)
		if e3 != nil {
			return "", fmt.Errorf("读取body失败: %w", e3)
		}
		return utils.CleanXMLContent(string(data)), nil
	})
	if err != nil {
		return cos.Article{}, fmt.Errorf("抓取失败(%s): %w", feedURL, err)
	}

	// 用 gofeed 解析 RSS
	parsed, err := utils.WithRetry(ctx, p.cfg.MaxRetries, p.cfg.RetryInterval, func() (*gofeed.Feed, error) {
		return p.parser.ParseString(body)
	})
	if err != nil {
		globalStats.mu.Lock()
		globalStats.ParseFailCount++
		globalStats.mu.Unlock()
		return cos.Article{}, fmt.Errorf("解析失败(%s): %w", feedURL, err)
	}

	// 域名
	domain, _ := p.extractDomain(parsed.Link)
	if domain == "" {
		domain = "unknown"
	}

	// 头像
	avatarURL := p.avatarMap[domain]
	if avatarURL == "" {
		globalStats.mu.Lock()
		globalStats.MissingAvatarCount++
		globalStats.MissingAvatarFeeds = append(globalStats.MissingAvatarFeeds, parsed.Title+" | "+feedURL)
		globalStats.mu.Unlock()

		// 用默认头像
		avatarURL = p.cfg.DefaultAvatarURL
		globalStats.mu.Lock()
		globalStats.UsedDefaultCount++
		globalStats.DefaultAvatarFeeds = append(globalStats.DefaultAvatarFeeds, parsed.Title+" | "+feedURL)
		globalStats.mu.Unlock()
	}

	// 最新文章
	if len(parsed.Items) == 0 {
		return cos.Article{}, fmt.Errorf("没有文章(%s)", feedURL)
	}
	item := parsed.Items[0]

	// 解析发布时间
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

	// 名称映射
	name := p.mapNameIfNeeded(parsed.Title)

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
mapNameIfNeeded：如果在 p.nameMap 里能找到短名称,则返回短名称,否则原样返回
*/
func (p *RSSProcessor) mapNameIfNeeded(longName string) string {
	if short, ok := p.nameMap[longName]; ok {
		return short
	}
	return longName
}

/*
extractDomain：解析 rawURL 返回 scheme://host
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
downloadFile：用 p.httpClient GET 下载一个文件内容
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载非200(%s): %d", fileURL, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取内容失败: %w", err)
	}
	return string(data), nil
}

/*
@function: saveArticlesToCOS
@description:
  - 序列化 articles 为 JSON 再上传至
  - 使用 withRetry 重试上传到 COSRSS
*/
func (p *RSSProcessor) saveArticlesToCOS(ctx context.Context, articles []cos.Article) error {
	// 序列化
	jsonData, err := json.Marshal(articles)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	// 使用 withRetry 重试PUT
	_, err = utils.WithRetry(ctx, p.cfg.MaxRetries, p.cfg.RetryInterval, func() (any, error) {
		resp, putErr := p.cosClient.Object.Put(ctx, p.cfg.COSRSS, bytes.NewReader(jsonData), nil)
		if putErr != nil {
			return nil, fmt.Errorf("COS 上传失败: %w", putErr)
		}
		_ = resp.Body.Close()
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("上传到COS失败: %w", err)
	}
	return nil
}
