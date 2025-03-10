// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: feed_fetcher.go
// 说明: 并发抓取RSS的核心逻辑, 包括获取RSS链接列表、抓取Feed等
//       并新增了对 "解析失败" 的重试机制，使用指数退避，不阻塞整体并发。

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

// fetchRSSLinks 从给定URL的文本文件中逐行读取RSS链接
// 【游钓四方 <haibao1027@gmail.com>】
// 作用:
//   - 发送HTTP GET请求获取一个纯文本文件
//   - 将文件内容按行拆分, 每行一个RSS链接
//
// 参数:
//   - rssListURL: 存放RSS链接的远程TXT文件URL
//
// 返回:
//   - []string: RSS链接数组
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
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	return links, nil
}

// fetchAllFeeds 并发抓取所有RSS链接, 返回结果列表
// 【游钓四方 <haibao1027@gmail.com>】
// 主要改动:
//   - 增加了 fetchFeedWithRetry 函数, 对 "解析RSS失败" 进行最多3次重试(指数退避).
//   - 重试的错误会直接使用 fmt.Printf 打印到 GitHub Actions 控制台, 不会写入日志文件.
//
// 参数:
//   - ctx           : 上下文
//   - rssLinks      : RSS链接切片
//   - defaultAvatar : 当无法获取到头像时使用的默认地址
//
// 返回:
//   - []feedResult         : 每个RSS的抓取结果(含成功或失败)
//   - map[string][]string  : 记录各种类型的问题(解析失败, 无内容, 头像空等)
func fetchAllFeeds(ctx context.Context, rssLinks []string, defaultAvatar string) ([]feedResult, map[string][]string) {
	// 最大并发数
	maxGoroutines := 10
	sem := make(chan struct{}, maxGoroutines)
	var wg sync.WaitGroup

	resultChan := make(chan feedResult, len(rssLinks))
	fp := gofeed.NewParser()

	for _, link := range rssLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{} // 占用并发槽

		go func(rssLink string) {
			defer wg.Done()
			defer func() { <-sem }() // 释放并发槽

			var fr feedResult
			fr.FeedLink = rssLink

			// ---------------------------
			// 1. 使用带重试的函数抓取RSS
			// ---------------------------
			feed, err := fetchFeedWithRetry(rssLink, fp, 3, 1*time.Second, 2.0)
			if err != nil {
				// 最终仍然失败, 则记为 "解析RSS失败"
				fr.Err = wrapErrorf(err, "解析RSS失败: %s", rssLink)
				resultChan <- fr
				return
			}

			// ---------------------------
			// 2. 判断是否为空Feed
			// ---------------------------
			if feed == nil || len(feed.Items) == 0 {
				fr.Err = wrapErrorf(fmt.Errorf("该订阅没有内容"), "RSS为空: %s", rssLink)
				resultChan <- fr
				return
			}

			// ---------------------------
			// 3. 获取头像
			// ---------------------------
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

			// ---------------------------
			// 4. 只取最新一篇
			// ---------------------------
			latest := feed.Items[0]
			fr.Article.Title = latest.Title
			fr.Article.Link = latest.Link

			// 尝试解析发布时间
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

			// ---------------------------
			// 5. 返回成功结果
			// ---------------------------
			resultChan <- fr
		}(link)
	}

	// 等待所有goroutine完成并收集结果
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 用于统计解析失败、无内容、头像空等问题
	problems := map[string][]string{
		"parseFails":   {}, // 解析RSS失败
		"feedEmpties":  {}, // RSS无内容
		"noAvatar":     {}, // 头像字段为空
		"brokenAvatar": {}, // 头像无法访问
	}

	var results []feedResult
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

		// 没有错误, 正常解析
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

// fetchFeedWithRetry 函数: 对 "解析RSS" 的过程进行重试
// 【游钓四方 <haibao1027@gmail.com>】
// 作用:
//   - 针对网络抖动或RSS源暂时不可用导致的解析失败, 可以进行多次尝试
//   - 重试时的错误只输出到GitHub Actions控制台, 不写入日志文件
//
// 参数:
//   - rssLink         : RSS链接
//   - parser          : gofeed.Parser对象
//   - maxRetries      : 最大重试次数(含首次尝试)
//   - baseWait        : 初始等待时间, 如1秒
//   - backoffMultiple : 指数退避的倍率, 如2.0表示每次翻倍
//
// 返回:
//   - *gofeed.Feed: 成功时返回解析后的Feed对象
//   - error       : 最终仍然失败时返回错误
func fetchFeedWithRetry(rssLink string, parser *gofeed.Parser, maxRetries int, baseWait time.Duration, backoffMultiple float64) (*gofeed.Feed, error) {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		var feed *gofeed.Feed
		var err error

		if i == 0 {
			// 第一次直接常规抓取
			feed, err = fetchFeed(rssLink, parser)
		} else {
			// 后续重试时，采用自定义 User-Agent + 忽略 SSL 证书 + 清洗非法字符
			feed, err = fetchFeedWithFix(rssLink, parser)
		}

		if err == nil {
			return feed, nil // 解析成功，直接返回
		}

		// 记录失败原因
		lastErr = err
		fmt.Printf("[Retry %d/%d] RSS parse fail for %s: %v\n", i+1, maxRetries, rssLink, err)

		// 指数退避（除最后一次外）
		if i < maxRetries-1 {
			wait := time.Duration(float64(baseWait) * math.Pow(backoffMultiple, float64(i)))
			time.Sleep(wait)
		}
	}

	return nil, lastErr
}

// fetchFeedWithFix 采用修复策略：自定义 User-Agent + 允许跳过 SSL + 清洗非法字符
func fetchFeedWithFix(rssLink string, parser *gofeed.Parser) (*gofeed.Feed, error) {
	// 自定义 HTTP 客户端，忽略 SSL 证书问题
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // 忽略证书问题
		},
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", rssLink, nil)
	if err != nil {
		return nil, err
	}

	// 设置自定义 User-Agent
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

// fetchFeed 函数: 先HTTP GET获取RSS内容, 去除非法XML字符后再用 gofeed.Parser 解析
// 【游钓四方 <haibao1027@gmail.com>】
// 作用:
//   - 解决极少数RSS源中出现类似 "illegal character code U+0008" 等控制字符导致XML报错
//   - 先将响应Body全部读取, 调用 removeInvalidXMLChars 函数清洗后, 再 parser.ParseString()
//
// 参数:
//   - rssLink : RSS链接
//   - parser  : gofeed.Parser 对象
//
// 返回:
//   - *gofeed.Feed : 成功时返回Feed对象, 否则error
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

	// 对RSS数据进行非法XML字符过滤，尤其是U+0008、U+0000等控制字符
	cleanData := removeInvalidXMLChars(rawData)
	return parser.ParseString(string(cleanData))
}

// removeInvalidXMLChars 函数: 去除常见非法的控制字符, 避免XML解析报错
// 【游钓四方 <haibao1027@gmail.com>】
// 说明:
//   - XML 1.0 对字符有严格限制, ASCII 0~8、11~12、14~31 都是不被允许的
//   - 这里简单做个过滤, 将这些字节全部剔除; 对大多数情况是安全的, 不会影响正常文本.
func removeInvalidXMLChars(data []byte) []byte {
	// 0x09(TAB)、0x0A(\n)、0x0D(\r) 在XML 1.0中是合法的控制字符, 其他 <0x20 的都非法
	// 参考: https://www.w3.org/TR/xml11/#charsets
	filtered := make([]byte, 0, len(data))
	for _, b := range data {
		if b == 0x09 || b == 0x0A || b == 0x0D || b >= 0x20 {
			filtered = append(filtered, b)
		}
	}
	return filtered
}
