// Author: 游钓四方 <haibao1027@gmail.com>
// File: feed_parser.go
// Description: 包含解析RSS时间字符串的函数、处理头像URL的函数等

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

// parseTime 尝试用多种格式解析RSS中的时间字符串, 若都失败则返回错误
//
// Description:
//
//	有些RSS的时间可能格式不同，此函数依次尝试多种日期格式进行解析，如果全部失败则返回错误
//
// Parameters:
//   - timeStr: 待解析的时间字符串
//
// Returns:
//   - time.Time: 解析成功后返回的时间
//   - error    : 如果所有格式都无法解析，则返回错误
func parseTime(timeStr string) (time.Time, error) {
	formats := []string{
		time.RFC1123Z,                   // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC1123,                    // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC3339,                    // "2006-01-02T15:04:05Z07:00"
		"2006-01-02T15:04:05.000Z07:00", // "2025-02-09T13:20:27.000Z"
		"Mon, 02 Jan 2006 15:04:05 +0000",
	}

	for _, f := range formats {
		if t, err := time.Parse(f, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

// getFeedAvatarURL 尝试从 feed.Image 或博客主页获取头像地址
//
// Description:
//
//	该函数首先尝试读取 feed.Image.URL，如果不存在，则尝试从博客主页（feed.Link）中解析常见的 icon 图标
//
// Parameters:
//   - feed: gofeed.Feed 指针, 其中包含 Image, Link等字段
//
// Returns:
//   - string: 若能获取到有效头像，则返回其URL；若无法获取则返回空字符串
func getFeedAvatarURL(feed *gofeed.Feed) string {
	// 优先使用 <Image> 标签内的 URL
	if feed.Image != nil && feed.Image.URL != "" {
		return feed.Image.URL
	}
	// 若没有 image 则尝试抓取博客主页获取
	if feed.Link != "" {
		return fetchBlogLogo(feed.Link)
	}
	return ""
}

// fetchBlogLogo 尝试抓取博客主页, 并从<head>中获取常见的 icon 或 meta og:image
//
// Description:
//
//	该函数通过 HTTP GET 请求获取博客首页内容，解析其 HTML，
//	在<head>标签中寻找<link rel="icon">或<meta property="og:image">等信息
//	如果解析失败或未找到，则回退到 favicon.ico
func fetchBlogLogo(blogURL string) string {
	// 如果获取失败，则直接回退到 favicon.ico
	resp, err := http.Get(blogURL)
	if err != nil {
		return fallbackFavicon(blogURL)
	}
	defer resp.Body.Close()

	// 如果状态码不是 200，视为获取主页失败
	if resp.StatusCode != 200 {
		return fallbackFavicon(blogURL)
	}

	// 解析HTML文档
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return fallbackFavicon(blogURL)
	}

	var iconHref, ogImage string

	// 递归函数，用来遍历 HTML 节点，寻找 <link> 和 <meta> 标签
	var f func(*html.Node)
	f = func(n *html.Node) {
		// 如果当前节点是元素节点
		if n.Type == html.ElementNode {
			// 获取当前节点的标签名称，并转为小写
			tagName := strings.ToLower(n.Data)

			// 针对 <link> 标签查找 iconHref
			if tagName == "link" {
				var relVal, hrefVal string
				for _, attr := range n.Attr {
					key := strings.ToLower(attr.Key)
					val := attr.Val
					if key == "rel" {
						relVal = strings.ToLower(val)
					}
					if key == "href" {
						hrefVal = val
					}
				}
				// 如果 rel 包含 icon 字段，并且 href 不为空，则视为站点图标
				if strings.Contains(relVal, "icon") && hrefVal != "" && iconHref == "" {
					iconHref = hrefVal
				}
			} else if tagName == "meta" {
				// 针对 <meta> 标签查找 og:image
				var propVal, contentVal string
				for _, attr := range n.Attr {
					key := strings.ToLower(attr.Key)
					val := attr.Val
					if key == "property" {
						propVal = strings.ToLower(val)
					}
					if key == "content" {
						contentVal = val
					}
				}
				if propVal == "og:image" && contentVal != "" {
					ogImage = contentVal
				}
			}
		}
		// 递归遍历子节点
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	// 如果找到 iconHref，则返回绝对路径
	if iconHref != "" {
		return makeAbsoluteURL(blogURL, iconHref)
	}
	// 如果没找到 <link rel="icon">，尝试使用 og:image
	if ogImage != "" {
		return makeAbsoluteURL(blogURL, ogImage)
	}
	// 仍未找到，则回退到 favicon.ico
	return fallbackFavicon(blogURL)
}

// fallbackFavicon 返回 "scheme://host/favicon.ico"
//
// Description:
//
//	如果在博客主页中找不到任何可用的 icon，就拼接 favicon.ico 路径
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
//
// Description:
//
//	如果给定的 refStr 是相对路径，则将其基于 baseStr 构造成绝对路径
//	如果本身就是绝对路径，则直接返回，常见于 HTML 中 <link>、<meta>、<img> 等的引用
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
//
// Description:
//
//	仅发送 HEAD 请求以确认资源是否存在且可访问，若返回状态码为200，则视为可用
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
	return resp.StatusCode == http.StatusOK, nil
}
