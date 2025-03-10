// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: feed_fetcher.go
// 说明: 并发抓取RSS的核心逻辑, 包括获取RSS链接列表、抓取Feed等
//       并新增了对 "解析失败" 的重试机制，使用指数退避，不阻塞整体并发。

package main

import (
	"context"
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

	// 尝试maxRetries次
	for i := 0; i < maxRetries; i++ {
		feed, err := parser.ParseURL(rssLink)
		if err == nil {
			// 如果解析成功, 直接返回feed
			return feed, nil
		}

		// 记录本次失败, 并在GitHub Actions控制台打印
		lastErr = err
		fmt.Printf("[Retry %d/%d] RSS parse fail for %s: %v\n",
			i+1, maxRetries, rssLink, err)

		// 如果还没到最后一次, 则等待一段时间后重试(指数退避)
		if i < maxRetries-1 {
			// 等待时间 = baseWait * (backoffMultiple ^ i)
			wait := time.Duration(float64(baseWait) * math.Pow(backoffMultiple, float64(i)))
			time.Sleep(wait)
		}
	}

	// 多次重试仍然失败, 返回最后一次的错误
	return nil, lastErr
}
