// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: feed_parser.go
// 说明: 包含解析RSS时间字符串的函数、处理头像URL的函数等

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed" // 确保go.mod里已引入
	"golang.org/x/net/html"
)

// parseTime 尝试用多种格式解析RSS中的时间字符串, 若都失败则返回错误
// 【游钓四方 <haibao1027@gmail.com>】
// 参数:
//   - timeStr: 时间字符串
//
// 返回:
//   - time.Time: 解析出的时间
//   - error    : 如果所有格式都失败, 返回错误; 否则为nil
func parseTime(timeStr string) (time.Time, error) {
	// 定义可能出现的多种时间格式
	formats := []string{
		time.RFC1123Z,                   // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC1123,                    // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC3339,                    // "2006-01-02T15:04:05Z07:00"
		"2006-01-02T15:04:05.000Z07:00", // "2025-02-09T13:20:27.000Z"
		"Mon, 02 Jan 2006 15:04:05 +0000",
	}

	// 依次尝试解析
	for _, f := range formats {
		if t, err := time.Parse(f, timeStr); err == nil {
			return t, nil
		}
	}
	// 如果都失败, 就返回错误
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

// getFeedAvatarURL 尝试从feed.Image或者博客主页获取头像地址
// 【游钓四方 <haibao1027@gmail.com>】
// 参数:
//   - feed: gofeed.Feed 指针, 其中包含Image, Link等字段
//
// 返回:
//   - string: 若能获取到有效头像, 则返回其URL; 若无法获取则返回空字符串
func getFeedAvatarURL(feed *gofeed.Feed) string {
	// 如果 RSS 中存在 <image> 标签且URL不为空, 则优先使用
	if feed.Image != nil && feed.Image.URL != "" {
		return feed.Image.URL
	}
	// 否则, 如果 feed.Link 不为空, 就尝试访问该链接获取头像
	if feed.Link != "" {
		return fetchBlogLogo(feed.Link)
	}
	// 如果以上都不行, 就返回空字符串
	return ""
}

// fetchBlogLogo 尝试抓取博客主页, 并从<head>中获取最常见的icon; 若没有则fallback到favicon.ico
// 【游钓四方 <haibao1027@gmail.com>】
func fetchBlogLogo(blogURL string) string {
	resp, err := http.Get(blogURL)
	if err != nil {
		return fallbackFavicon(blogURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fallbackFavicon(blogURL)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return fallbackFavicon(blogURL)
	}

	var iconHref string
	var ogImage string

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tagName := strings.ToLower(n.Data)
			// 处理<link ...>
			if tagName == "link" {
				var relVal, hrefVal string
				for _, attr := range n.Attr {
					key := strings.ToLower(attr.Key)
					val := attr.Val
					switch key {
					case "rel":
						relVal = strings.ToLower(val)
					case "href":
						hrefVal = val
					}
				}
				if strings.Contains(relVal, "icon") && hrefVal != "" {
					if iconHref == "" {
						iconHref = hrefVal
					}
				}
			} else if tagName == "meta" {
				// 处理<meta ...>
				var propVal, contentVal string
				for _, attr := range n.Attr {
					key := strings.ToLower(attr.Key)
					val := attr.Val
					switch key {
					case "property":
						propVal = strings.ToLower(val)
					case "content":
						contentVal = val
					}
				}
				if propVal == "og:image" && contentVal != "" {
					ogImage = contentVal
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	if iconHref != "" {
		return makeAbsoluteURL(blogURL, iconHref)
	}
	if ogImage != "" {
		return makeAbsoluteURL(blogURL, ogImage)
	}
	return fallbackFavicon(blogURL)
}

// fallbackFavicon 返回 "scheme://host/favicon.ico"
// 【游钓四方 <haibao1027@gmail.com>】
func fallbackFavicon(blogURL string) string {
	u, err := url.Parse(blogURL)
	if err != nil {
		return ""
	}
	if u.Scheme == "" || u.Host == "" {
		return ""
	}
	return fmt.Sprintf("%s://%s/favicon.ico", u.Scheme, u.Host)
}

// makeAbsoluteURL 将相对路径转换为绝对路径
// 【游钓四方 <haibao1027@gmail.com>】
func makeAbsoluteURL(baseStr, refStr string) string {
	baseURL, err := url.Parse(baseStr)
	if err != nil {
		return refStr
	}
	refURL, err := url.Parse(refStr)
	if err != nil {
		return refStr
	}
	return baseURL.ResolveReference(refURL).String()
}

// checkURLAvailable 通过HEAD请求检查URL是否可正常访问(返回200)
// 【游钓四方 <haibao1027@gmail.com>】
func checkURLAvailable(urlStr string) (bool, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	req, err := http.NewRequest("HEAD", urlStr, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return (resp.StatusCode == http.StatusOK), nil
}
