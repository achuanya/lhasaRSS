// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: feed_fetcher.go
// 说明: 并发抓取RSS的核心逻辑, 包括获取RSS链接列表、抓取Feed等

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

// fetchRSSLinks 从给定URL的文本文件中逐行读取RSS链接
// 【游钓四方 <haibao1027@gmail.com>】
func fetchRSSLinks(rssListURL string) ([]string, error) {
	resp, err := http.Get(rssListURL)
	if err != nil {
		return nil, wrapErrorf(err, "无法获取RSS列表文件: %s", rssListURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, wrapErrorf(fmt.Errorf("HTTP状态码: %d", resp.StatusCode),
			"获取RSS列表失败: %s", rssListURL)
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
// 参数:
//   - ctx: 上下文
//   - rssLinks: RSS链接切片
//   - defaultAvatar: 当无法获取到头像时使用的默认地址
//
// 返回:
//   - []feedResult: 每个RSS的抓取结果
//   - map[string][]string: 记录各种类型的问题(解析失败, 无内容, 头像空等)
func fetchAllFeeds(ctx context.Context, rssLinks []string, defaultAvatar string) ([]feedResult, map[string][]string) {
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
		sem <- struct{}{}

		go func(rssLink string) {
			defer wg.Done()
			defer func() { <-sem }()

			var fr feedResult
			fr.FeedLink = rssLink

			feed, err := fp.ParseURL(rssLink)
			if err != nil {
				fr.Err = wrapErrorf(err, "解析RSS失败: %s", rssLink)
				resultChan <- fr
				return
			}
			if feed == nil || len(feed.Items) == 0 {
				fr.Err = wrapErrorf(fmt.Errorf("该订阅没有内容"), "RSS为空: %s", rssLink)
				resultChan <- fr
				return
			}

			// 获取头像
			avatarURL := getFeedAvatarURL(feed)

			// 构造Article
			fr.Article = &Article{
				BlogName: feed.Title,
			}

			// 判断头像可用性
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

			// 只取最新一篇
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

			resultChan <- fr
		}(link)
	}

	// 等待完成并收集结果
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	var results []feedResult
	problems := map[string][]string{
		"parseFails":   {}, // 解析RSS失败
		"feedEmpties":  {}, // RSS无内容
		"noAvatar":     {}, // 头像字段为空
		"brokenAvatar": {}, // 头像无法访问
	}

	for r := range resultChan {
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

		// 正常获取
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
