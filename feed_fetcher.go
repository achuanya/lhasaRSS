// Author: 游钓四方 <haibao1027@gmail.com>
// File: feed_fetcher.go
// Description:
//   并发抓取RSS Feed的核心逻辑，包括：
//   1. 从COS或本地文件获取RSS文件
//   2. 并发抓取每个RSS Feed
//   3. 对解析失败的RSS使用指数退避算法进行重试

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// fetchRSSLinks 根据 cfg.RssSource 选择从COS拉取txt还是读取本地文件
//
// Description:
//
//	若 cfg.RssSource = "COS"，则通过 http.Get(cfg.RssListURL) 获取RSS列表txt
//	若 cfg.RssSource = "GITHUB"，则认为 cfg.RssListURL 指向本地文件路径，直接 os.ReadFile
//	读到内容后按行分割，去掉空行，返回 RSS 链接列表
func fetchRSSLinks(cfg *Config) ([]string, error) {
	switch cfg.RssSource {
	case "COS":
		return fetchRSSLinksFromHTTP(cfg.RssListURL)
	case "GITHUB":
		return fetchRSSLinksFromLocal(cfg.RssListURL)
	default:
		return nil, fmt.Errorf("无效的 RSS_SOURCE 配置: %s", cfg.RssSource)
	}
}

// fetchRSSLinksFromHTTP 从远程 TXT 文件中逐行读取 RSS 链接
//
// Description:
//
//	通过 HTTP GET 请求获取存放在 COS (或其他 URL ) 中的一个纯文本文件（每行一个RSS链接）
//	然后将这些链接按行分割返回
func fetchRSSLinksFromHTTP(rssTxtURL string) ([]string, error) {
	resp, err := http.Get(rssTxtURL)
	if err != nil {
		return nil, wrapErrorf(err, "无法获取RSS列表文件: %s", rssTxtURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, wrapErrorf(
			fmt.Errorf("HTTP状态码: %d", resp.StatusCode),
			"获取RSS列表失败: %s", rssTxtURL,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapErrorf(err, "读取RSS列表body失败")
	}

	return parseLinesToLinks(data), nil
}

// fetchRSSLinksFromLocal 从本地文件中逐行读取RSS链接
//
// Description:
//
//	从 Github 读取文本内容，然后将其按行分割返回
func fetchRSSLinksFromLocal(filePath string) ([]string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, wrapErrorf(err, "读取Github RSS文件失败: %s", filePath)
	}
	return parseLinesToLinks(data), nil
}

// parseLinesToLinks 将字节切片按行拆分并去掉空白行, 返回非空字符串切片
func parseLinesToLinks(data []byte) []string {
	var links []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	return links
}

// fetchAllFeeds 并发抓取所有RSS链接，返回抓取结果及统计信息
//
// Description:
//
//	该函数读取传入的所有RSS链接，使用10路并发进行抓取
//	在抓取过程中对解析失败、内容为空等情况进行统计
//	若抓取的RSS头像缺失或无法访问，将替换为默认头像
//	支持通过AvatarMapper进行域名匹配和头像替换
//
// Parameters:
//   - ctx           : 上下文，用于控制网络请求的取消或超时
//   - rssLinks      : RSS链接的字符串切片，每个链接代表一个RSS源
//   - defaultAvatar : 备用头像地址，在抓取头像失败或不可用时使用
//   - avatarMapper  : 头像映射器，用于根据域名替换头像
//
// Returns:
//   - []feedResult         : 每个RSS链接抓取的结果（包含成功的Feed及其文章或错误信息）
//   - map[string][]string  : 各种问题的统计记录（解析失败、内容为空、头像缺失、头像不可用）
func fetchAllFeeds(ctx context.Context, rssLinks []string, defaultAvatar string, avatarMapper *AvatarMapper) ([]feedResult, map[string][]string) {
	// 设置最大并发量，以信道（channel）信号量的方式控制
	maxGoroutines := 10
	sem := make(chan struct{}, maxGoroutines)

	// 等待组，用来等待所有goroutine执行完毕
	var wg sync.WaitGroup

	resultChan := make(chan feedResult, len(rssLinks)) // 用于收集抓取结果的通道
	fp := gofeed.NewParser()                           // RSS解析器实例

	// 遍历所有RSS链接，为每个RSS链接开启一个goroutine进行抓取
	for _, link := range rssLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		wg.Add(1)         // 每开启一个goroutine，对应Add(1)
		sem <- struct{}{} // 向sem发送一个空结构体，表示占用了一个并发槽

		// 开启协程
		go func(rssLink string) {
			defer wg.Done()          // 协程结束时Done
			defer func() { <-sem }() // 函数结束时释放一个并发槽

			var fr feedResult
			fr.FeedLink = rssLink

			// 抓取RSS Feed, 无法解析时，使用指数退避算法进行重试, 有3次重试, 初始1s, 倍数2.0
			feed, err := fetchFeedWithRetry(rssLink, fp, 3, 1*time.Second, 2.0)
			if err != nil {
				// 如果解析失败，记录错误并把结果发送到通道
				fr.Err = wrapErrorf(err, "解析RSS失败: %s", rssLink)
				resultChan <- fr
				return
			}

			// 如果Feed为空或没有Items，视作无有效内容
			if feed == nil || len(feed.Items) == 0 {
				fr.Err = wrapErrorf(fmt.Errorf("该订阅没有内容"), "RSS为空: %s", rssLink)
				resultChan <- fr
				return
			}

			// 获取RSS的头像信息（若RSS自带头像则用RSS的，否则尝试从博客主页解析）
			avatarURL := getFeedAvatarURL(feed)
			fr.Article = &Article{
				BlogName: feed.Title, // 记录博客名称
			}

			// 检查头像可用性
			if avatarURL == "" {
				// 若头像链接为空，则标记为空字符串
				fr.Article.Avatar = ""
			} else {
				ok, _ := checkURLAvailable(avatarURL)
				if !ok {
					fr.Article.Avatar = "BROKEN" // 无法访问，暂记为BROKEN
				} else {
					fr.Article.Avatar = avatarURL // 正常可访问则记录真实URL
				}
			}

			// 只取最新一篇文章作为结果
			latest := feed.Items[0]
			fr.Article.Title = latest.Title
			fr.Article.Link = latest.Link

			// 解析发布时间，如果 RSS 解析器本身给出了 PublishedParsed 直接用，否则尝试解析 Published 字符串
			pubTime := time.Now()
			if latest.PublishedParsed != nil {
				pubTime = *latest.PublishedParsed
			} else if latest.Published != "" {
				if t, e := parseTime(latest.Published); e == nil {
					pubTime = t
				}
			}
			// 把解析出的时间，格式化为 "Jan 02, 2006" 记录下来
			fr.ParsedTime = pubTime
			fr.Article.Published = pubTime.Format("Jan 02, 2006")

			resultChan <- fr
		}(link)
	}

	// 开启一个goroutine等待所有抓取任务结束后，关闭resultChan
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 用于统计各种问题
	problems := map[string][]string{
		"parseFails":   {}, // 解析 RSS 失败
		"feedEmpties":  {}, // 内容 RSS 为空
		"noAvatar":     {}, // 头像地址为空
		"brokenAvatar": {}, // 头像无法访问
	}
	// 收集抓取结果
	var results []feedResult

	for r := range resultChan {
		if r.Err != nil {
			// 若存在错误，进一步识别错误类型以便统计
			errStr := r.Err.Error()
			switch {
			case strings.Contains(errStr, "解析RSS失败"):
				problems["parseFails"] = append(problems["parseFails"], r.FeedLink)
			case strings.Contains(errStr, "RSS为空"):
				problems["feedEmpties"] = append(problems["feedEmpties"], r.FeedLink)
			}
			results = append(results, r)
			continue
		}

		// 对于成功抓取的Feed，如果头像为空或不可用则使用默认头像
		// 首先尝试使用AvatarMapper进行域名匹配替换
        if avatarMapper != nil {
            if mappedAvatar, found := avatarMapper.GetAvatarByURL(r.FeedLink); found {
                r.Article.Avatar = mappedAvatar
            }
            if mappedName, found := avatarMapper.GetNameByURL(r.FeedLink); found {
                r.Article.BlogName = mappedName
            }
        }

		if r.Article.Avatar == "" {
			problems["noAvatar"] = append(problems["noAvatar"], r.FeedLink)
			r.Article.Avatar = defaultAvatar
		} else if r.Article.Avatar == "BROKEN" {
			problems["brokenAvatar"] = append(problems["brokenAvatar"], r.FeedLink)
			r.Article.Avatar = defaultAvatar
		}
		results = append(results, r)
	}
	return results, problems
}

// fetchFeedWithRetry 对单个RSS链接进行抓取，在解析失败时，使用指数退避算法进行多次重试
//
// Description:
//
//	本函数会在解析RSS失败时，进行多次尝试：第一次直接常规抓取；后续使用自定义User-Agent、忽略SSL问题、清理非法XML字符的方法，
//	并在每次失败后等待一定时长，等待时长使用指数退避（backoffMultiple）
//
// Parameters:
//   - rssLink         : RSS链接
//   - parser          : gofeed.Parser实例，用于解析RSS数据
//   - maxRetries      : 最大尝试次数（包含首次尝试）
//   - baseWait        : 初始等待时长（如1秒）
//   - backoffMultiple : 每次重试等待时间的增长倍数（如2.0，即每次等待时间翻倍）
//
// Returns:
//   - *gofeed.Feed:  成功时返回解析后的Feed对象
//   - error       :  若所有重试均失败，则返回最后一次的错误
func fetchFeedWithRetry(rssLink string, parser *gofeed.Parser, maxRetries int, baseWait time.Duration, backoffMultiple float64) (*gofeed.Feed, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		var feed *gofeed.Feed
		var err error

		// 第一次尝试使用常规抓取
		if i == 0 {
			feed, err = fetchFeed(rssLink, parser)
		} else {
			// 后续重试时，使用“忽略SSL、自定义UA、清理数据”的抓取方式
			feed, err = fetchFeedWithFix(rssLink, parser)
		}

		if err == nil {
			// 如果本次尝试成功解析，则直接返回
			return feed, nil
		}
		lastErr = err

		fmt.Printf("[Retry %d/%d] RSS parse fail for %s: %v\n", i+1, maxRetries, rssLink, err)

		// 若还未到最后一次尝试，则等待一段时间后继续重试
		if i < maxRetries-1 {
			wait := time.Duration(float64(baseWait) * math.Pow(backoffMultiple, float64(i)))
			time.Sleep(wait)
		}
	}
	return nil, lastErr
}

// fetchFeed 使用最简单的 http.Get 抓取RSS，并在需要时去除非法XML字符
//
// Description:
//
//	常规抓取方式，只做了基础的 http.Get 请求和非法字符清理，通常是第一优先使用的方法，
//	在失败后才会使用 fetchFeedWithFix
//
// Parameters:
//   - rssLink : RSS链接
//   - parser  : gofeed.Parser实例
//
// Returns:
//   - *gofeed.Feed : 成功时返回Feed对象
//   - error        : 若请求或解析失败，则返回错误信息
func fetchFeed(rssLink string, parser *gofeed.Parser) (*gofeed.Feed, error) {
	resp, err := http.Get(rssLink)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 状态码不为200，视为失败
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 去除非法的 XML 控制字符，避免解析错误
	cleanData := removeInvalidXMLChars(rawData)
	return parser.ParseString(string(cleanData))
}

// fetchFeedWithFix 采用修复策略抓取RSS
//
// Description:
//
//	在抓取失败后，才会进行这一步的尝试
//	1. 跳过不安全的SSL验证
//	2. 自定义请求头 User-Agent
//	3. 读取后再移除非法的 XML 控制字符
//
// Parameters:
//   - rssLink : RSS链接地址
//   - parser  : gofeed.Parser 实例，用于解析RSS数据
//
// Returns:
//   - *gofeed.Feed: 解析后的Feed对象
//   - error       : 若抓取或解析失败，则返回错误
func fetchFeedWithFix(rssLink string, parser *gofeed.Parser) (*gofeed.Feed, error) {
	// 自定义HTTP客户端，允许跳过SSL证书验证，超时10秒
	client := &http.Client{
		Transport: &http.Transport{
			// InsecureSkipVerify: true 表示跳过对证书合法性的检测
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}

	// 构造请求并设置自定义User-Agent
	req, err := http.NewRequest("GET", rssLink, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSSFetcher/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 如果状态码不是 200，视为获取失败
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	// 读取响应数据
	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 移除响应中的非法XML字符
	cleanData := removeInvalidXMLChars(rawData)
	return parser.ParseString(string(cleanData))
}

// removeInvalidXMLChars 过滤掉数据中非法的XML控制字符
//
// Description:
//
//	用于去掉 < 0x20 但又不是 \t, \n, \r 的不可见字符，这些字符会导致 XML 解析失败
//
// Parameters:
//   - data: 原始字节数据
//
// Returns:
//   - []byte: 过滤后的合法数据
func removeInvalidXMLChars(data []byte) []byte {
	// 只保留合法的字符：\t, \n, \r, >= 0x20
	filtered := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0A || b == 0x0D || b >= 0x20 {
			filtered = append(filtered, b)
		}
	}
	return filtered
}
