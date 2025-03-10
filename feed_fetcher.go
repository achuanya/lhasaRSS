// Author: 游钓四方 <haibao1027@gmail.com>
// File: feed_fetcher.go
// Description:
//   并发抓取RSS Feed的核心逻辑，包括：
//   1. 从COS中获取RSS文件
//   2. 并发抓取每个RSS Feed
//   3. 对解析失败的RSS使用指数退避策略进行重试

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// fetchRSSLinks 从COS的TXT文件中逐行读取RSS链接
// Parameters:
//   - rssListURL: COS中的TXT文件，每行一个URL
//
// Returns:
//   - []string: 包含所有RSS链接的字符串切片
//   - error   : 出错时返回错误, 否则nil
func fetchRSSLinks(rssListURL string) ([]string, error) {
	resp, err := http.Get(rssListURL)
	if err != nil {
		return nil, wrapErrorf(err, "无法获取RSS列表文件: %s", rssListURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, wrapErrorf(
			fmt.Errorf("HTTP状态码: %d", resp.StatusCode),
			"获取RSS列表失败: %s", rssListURL,
		)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapErrorf(err, "读取RSS列表body时失败")
	}

	var links []string
	// 按行拆分文本，并过滤掉空行
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	return links, nil
}

// fetchAllFeeds 并发抓取所有RSS链接, 并返回每个链接的抓取结果及抓取过程中出现的问题统计
// Purpose:
//   - ctx           : 上下文，用于控制取消或超时
//   - rssLinks      : RSS链接的字符串切片
//   - defaultAvatar : 当获取Feed中头像失败时，使用的默认头像地址
//
// Returns:
//   - []feedResult         : 每个RSS链接抓取的结果（包含成功的Feed或错误信息）
//   - map[string][]string  : 针对解析失败、空Feed、头像缺失的统计记录
func fetchAllFeeds(ctx context.Context, rssLinks []string, defaultAvatar string) ([]feedResult, map[string][]string) {
	// 限制同时并发抓取的最大协程数
	maxGoroutines := 10
	sem := make(chan struct{}, maxGoroutines)
	var wg sync.WaitGroup

	resultChan := make(chan feedResult, len(rssLinks))
	fp := gofeed.NewParser()

	// 遍历所有RSS链接，开启协程进行抓取
	for _, link := range rssLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{} // 占用一个并发槽

		go func(rssLink string) {
			defer wg.Done()
			defer func() { <-sem }() // 释放并发槽

			var fr feedResult
			fr.FeedLink = rssLink

			// 使用带重试机制的函数抓取RSS Feed
			feed, err := fetchFeedWithRetry(rssLink, fp, 3, 1*time.Second, 2.0)
			if err != nil {
				fr.Err = wrapErrorf(err, "解析RSS失败: %s", rssLink)
				resultChan <- fr
				return
			}

			// 检查Feed内容是否为空
			if feed == nil || len(feed.Items) == 0 {
				fr.Err = wrapErrorf(fmt.Errorf("该订阅没有内容"), "RSS为空: %s", rssLink)
				resultChan <- fr
				return
			}

			// 获取Feed的头像信息
			avatarURL := getFeedAvatarURL(feed)
			fr.Article = &Article{
				BlogName: feed.Title,
			}

			// 检查头像可用性
			if avatarURL == "" {
				fr.Article.Avatar = ""
			} else {
				ok, _ := checkURLAvailable(avatarURL)
				if !ok {
					fr.Article.Avatar = "BROKEN"
				} else {
					fr.Article.Avatar = avatarURL
				}
			}

			// 只取最新一篇文章作为抓取结果
			latest := feed.Items[0]
			fr.Article.Title = latest.Title
			fr.Article.Link = latest.Link

			// 尝试解析发布时间，优先使用已解析的时间，其次尝试解析字符串格式的发布时间
			pubTime := time.Now()
			if latest.PublishedParsed != nil {
				pubTime = *latest.PublishedParsed
			} else if latest.Published != "" {
				if t, e := parseTime(latest.Published); e == nil {
					pubTime = t
				}
			}
			fr.ParsedTime = pubTime
			fr.Article.Published = pubTime.Format("02 Jan 2006")

			// 返回成功抓取的结果
			resultChan <- fr
		}(link)
	}

	// 等待所有goroutine完成并收集结果
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 初始化问题统计：解析失败、Feed为空、头像缺失
	problems := map[string][]string{
		"parseFails":   {}, // 解析RSS失败
		"feedEmpties":  {}, // RSS无内容
		"noAvatar":     {}, // 头像字段为空
		"brokenAvatar": {}, // 头像无法访问
	}

	var results []feedResult
	// 汇总每个RSS抓取的结果，并更新统计
	for r := range resultChan {
		// 若有错误
		if r.Err != nil {
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

		// 对于成功抓取的Feed，若头像为空或不可用则替换为默认头像，并记录到日志
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
// Purpose:
//   - rssLink         : RSS链接
//   - parser          : gofeed.Parser实例，用于解析RSS数据
//   - maxRetries      : 最大尝试次数（包含首次尝试）
//   - baseWait        : 初始等待时长（如1秒）
//   - backoffMultiple : 每次重试等待时间的增长倍数（如2.0，即每次等待时间翻倍）
//
// Returns:
//   - *gofeed.Feed: 成功时返回解析后的Feed对象
//   - error       : 若所有重试均失败，则返回最后一次的错误信息
func fetchFeedWithRetry(rssLink string, parser *gofeed.Parser, maxRetries int, baseWait time.Duration, backoffMultiple float64) (*gofeed.Feed, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		var feed *gofeed.Feed
		var err error

		if i == 0 {
			// 第一次尝试直接使用常规抓取方法
			feed, err = fetchFeed(rssLink, parser)
		} else {
			// 后续重试时采用：自定义User-Agent、忽略SSL问题、清洗非法字符
			feed, err = fetchFeedWithFix(rssLink, parser)
		}

		if err == nil {
			return feed, nil // 解析成功，直接返回
		}

		lastErr = err
		fmt.Printf("[Retry %d/%d] RSS parse fail for %s: %v\n", i+1, maxRetries, rssLink, err)

		// 若未到最后一次重试，则等待一定时间后再重试
		if i < maxRetries-1 {
			wait := time.Duration(float64(baseWait) * math.Pow(backoffMultiple, float64(i)))
			time.Sleep(wait)
		}
	}

	return nil, lastErr
}

// fetchFeedWithFix 采用修复策略抓取RSS：
//   - 使用自定义HTTP客户端忽略SSL证书问题
//   - 设置自定义User-Agent
//   - 清洗响应数据中的非法XML字符
//
// Purpose:
//   - rssLink : RSS链接地址
//   - parser  : gofeed.Parser实例，用于解析RSS数据
//
// Returns:
//   - *gofeed.Feed: 解析后的Feed对象
//   - error       : 若抓取或解析失败，则返回错误信息
func fetchFeedWithFix(rssLink string, parser *gofeed.Parser) (*gofeed.Feed, error) {
	// 自定义HTTP客户端，允许跳过SSL证书验证
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", rssLink, nil)
	if err != nil {
		return nil, err
	}

	// 设置自定义User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; RSSFetcher/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 失败后才执行非法字符清洗
	cleanData := removeInvalidXMLChars(rawData)
	return parser.ParseString(string(cleanData))
}

// fetchFeed 触发指数退避算法时，去除非法XML字符，再用 gofeed.Parser 解析
// Purpose:
//   - rssLink : RSS链接
//   - parser  : gofeed.Parser实例，用于解析RSS数据
//
// Returns:
//   - *gofeed.Feed : 成功时返回Feed对象, 否则error
//   - error        : 若请求或解析失败，则返回错误信息
func fetchFeed(rssLink string, parser *gofeed.Parser) (*gofeed.Feed, error) {
	resp, err := http.Get(rssLink)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http error: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	rawData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 过滤RSS数据中的非法XML控制字符（例如U+0008、U+0000等）
	cleanData := removeInvalidXMLChars(rawData)
	return parser.ParseString(string(cleanData))
}

// removeInvalidXMLChars 过滤掉数据中非法的XML控制字符，避免解析错误
// Purpose:
//   - data: 原始字节数据
//
// Returns:
//   - []byte: 过滤后的数据
func removeInvalidXMLChars(data []byte) []byte {
	filtered := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0A || b == 0x0D || b >= 0x20 {
			filtered = append(filtered, b)
		}
	}
	return filtered
}
