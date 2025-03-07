package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tencentyun/cos-go-sdk-v5"

	"golang.org/x/net/html"
)

/* -------------------- 数据结构 -------------------- */

// Article 只保留最关键字段
type Article struct {
	BlogName  string `json:"blog_name"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Link      string `json:"link"`
	Avatar    string `json:"avatar"` // 博客头像
}

type AllData struct {
	Items   []Article `json:"items"`
	Updated time.Time `json:"updated"`
}

/* -------------------- 头像处理：RSS -> 如无则抓主页面 HTML -> fallback favicon.ico -------------------- */

// getFeedAvatarURL 先看 RSS feed.Image 是否有值，否则就去抓主页面 HTML
func getFeedAvatarURL(feed *gofeed.Feed) string {
	// 如果 RSS 里有 <image> 标签
	if feed.Image != nil && feed.Image.URL != "" {
		return feed.Image.URL
	}
	// 否则访问 feed.Link (如果有)
	if feed.Link == "" {
		return ""
	}
	// 抓取页面 <head> 中可能的 icon 标签
	avatar := fetchBlogLogo(feed.Link)
	return avatar
}

// fetchBlogLogo 尝试访问给定链接（通常是博客主页），解析 HTML <head> 寻找最常见的 icon；如果都没有就返回 domain/favicon.ico
func fetchBlogLogo(blogURL string) string {
	// 1. 先请求该页面
	resp, err := http.Get(blogURL)
	if err != nil {
		// 如果请求失败，就尝试 fallback 到 domain + /favicon.ico
		return fallbackFavicon(blogURL)
	}
	defer resp.Body.Close()

	// 如果不是 200，依然尝试 fallback
	if resp.StatusCode != 200 {
		return fallbackFavicon(blogURL)
	}

	// 2. 解析 HTML，找 <link rel="icon" ...> / <link rel="shortcut icon"> / <link rel="apple-touch-icon"> / <meta property="og:image">
	doc, err := html.Parse(resp.Body)
	if err != nil {
		return fallbackFavicon(blogURL)
	}

	// 在 <head> 里找可能的 icon link
	var iconHref string
	var ogImage string

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			tagName := strings.ToLower(n.Data)
			if tagName == "link" {
				var relVal, hrefVal string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "rel":
						relVal = strings.ToLower(attr.Val)
					case "href":
						hrefVal = attr.Val
					}
				}
				// 如果 rel 是 icon / shortcut icon / apple-touch-icon
				if (strings.Contains(relVal, "icon")) && hrefVal != "" {
					// 优先记住第一个匹配到的 icon
					if iconHref == "" {
						iconHref = hrefVal
					}
				}
			} else if tagName == "meta" {
				// <meta property="og:image" content="...">
				var propVal, contentVal string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "property":
						propVal = strings.ToLower(attr.Val)
					case "content":
						contentVal = attr.Val
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

	// 优先用 link rel="icon" / "shortcut icon" / "apple-touch-icon"
	if iconHref != "" {
		return makeAbsoluteURL(blogURL, iconHref)
	}
	// 再试 og:image
	if ogImage != "" {
		return makeAbsoluteURL(blogURL, ogImage)
	}
	// 都没有就 fallback
	return fallbackFavicon(blogURL)
}

// fallbackFavicon 解析 domain，返回 domain + /favicon.ico
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

// makeAbsoluteURL 用于把相对路径拼成绝对路径
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

/* -------------------- GitHub 日志写入相关 -------------------- */

func getGitHubFileSHA(ctx context.Context, token, owner, repo, path string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	var response struct {
		SHA string `json:"sha"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	return response.SHA, nil
}

func getGitHubFileContent(ctx context.Context, token, owner, repo, path string) (string, string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", "", nil
	}
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	var response struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", "", err
	}

	decoded, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return "", "", err
	}
	return string(decoded), response.SHA, nil
}

func putGitHubFile(ctx context.Context, token, owner, repo, path, sha, content, commitMsg, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	payload := map[string]interface{}{
		"message": commitMsg,
		"content": encoded,
		"branch":  "main", // 如果分支是 master，请改成 "master"
		"committer": map[string]string{
			"name":  committerName,
			"email": committerEmail,
		},
	}
	if sha != "" {
		payload["sha"] = sha
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to put file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func deleteGitHubFile(ctx context.Context, token, owner, repo, path, sha, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	payload := map[string]interface{}{
		"message": "Delete old log file",
		"sha":     sha,
		"branch":  "main",
		"committer": map[string]string{
			"name":  committerName,
			"email": committerEmail,
		},
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func listGitHubDir(ctx context.Context, token, owner, repo, dir string) ([]struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
	Type string `json:"type"`
}, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, dir)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list dir %s, status: %d, body: %s", dir, resp.StatusCode, string(bodyBytes))
	}

	var files []struct {
		Name string `json:"name"`
		SHA  string `json:"sha"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

/*
appendLog:
- 不再拼接 repo = owner + "/" + repoName
- 日志附带时间戳
*/
func appendLog(ctx context.Context, rawLogContent string) error {
	token := os.Getenv("TOKEN")
	githubUser := os.Getenv("NAME")     // GitHub 用户名
	repoName := os.Getenv("REPOSITORY") // 仓库名，例如 "lhasaRSS"
	owner := githubUser
	repo := repoName // 不再拼接

	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	// 日志文件形如 logs/2025-03-07.log
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join("logs", dateStr+".log")

	// 1. 先获取旧内容(如果有)
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 2. 生成带时间戳的新日志
	var sb strings.Builder
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	lines := strings.Split(rawLogContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, line))
	}
	newLogSegment := sb.String()

	newContent := oldContent + newLogSegment

	// 3. 上传到 GitHub
	if err := putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent,
		"Update log: "+dateStr, committerName, committerEmail,
	); err != nil {
		return err
	}

	// 4. 清理 7 天前的日志
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

func cleanOldLogs(ctx context.Context, token, owner, repo, committerName, committerEmail string) error {
	files, err := listGitHubDir(ctx, token, owner, repo, "logs")
	if err != nil {
		return nil
	}
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)

	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}\.log$`, f.Name)
		if !matched {
			continue
		}
		dateStr := strings.TrimSuffix(f.Name, ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if t.Before(sevenDaysAgo) {
			path := filepath.Join("logs", f.Name)
			delErr := deleteGitHubFile(ctx, token, owner, repo, path, f.SHA, committerName, committerEmail)
			if delErr != nil {
				fmt.Printf("删除旧日志 %s 失败: %v\n", f.Name, delErr)
			} else {
				fmt.Printf("已删除旧日志 %s\n", f.Name)
			}
		}
	}
	return nil
}

/* -------------------- 主逻辑：只抓最新一篇 & 头像优先 feed.Image 否则抓主页 logo -------------------- */

func main() {
	ctx := context.Background()

	// 读取必要的环境变量
	secretID := os.Getenv("TENCENT_CLOUD_SECRET_ID")
	secretKey := os.Getenv("TENCENT_CLOUD_SECRET_KEY")
	rssListURL := os.Getenv("RSS")
	dataURL := os.Getenv("DATA")

	fmt.Println(">> Debug: secretID=", secretID)
	fmt.Println(">> Debug: secretKey=", secretKey)
	fmt.Println(">> Debug: RSS=", rssListURL)
	fmt.Println(">> Debug: DATA=", dataURL)
	fmt.Println(">> Debug: TOKEN=", os.Getenv("TOKEN"))
	fmt.Println(">> Debug: NAME=", os.Getenv("NAME"))
	fmt.Println(">> Debug: REPOSITORY=", os.Getenv("REPOSITORY"))

	if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
		_ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置。")
		return
	}

	// 1. 拉取 RSS 链接列表
	rssLinks, err := fetchRSSLinks(rssListURL)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取 RSS 列表文件失败: %v", err))
		return
	}
	if len(rssLinks) == 0 {
		_ = appendLog(ctx, "[WARN] RSS 列表为空。")
		return
	}

	// 2. 逐个订阅，只抓最新文章
	fp := gofeed.NewParser()
	var allItems []Article
	var failedList []string

	for _, link := range rssLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		feed, err := fp.ParseURL(link)
		if err != nil {
			failedList = append(failedList, fmt.Sprintf("订阅链接: %s，错误: %v", link, err))
			continue
		}
		if feed == nil || len(feed.Items) == 0 {
			// 没有任何文章就跳过
			continue
		}

		avatarURL := getFeedAvatarURL(feed)

		// 只拿最新一篇
		latest := feed.Items[0] // 如果想更精准可先对 feed.Items 排序
		article := Article{
			BlogName:  feed.Title,
			Title:     latest.Title,
			Published: latest.Published,
			Link:      latest.Link,
			Avatar:    avatarURL,
		}
		allItems = append(allItems, article)
	}

	// 3. 若需要统一按发布时间排序
	sort.Slice(allItems, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC1123Z, allItems[i].Published)
		tj, _ := time.Parse(time.RFC1123Z, allItems[j].Published)
		return ti.After(tj)
	})

	// 4. 组装最终 JSON
	allData := AllData{
		Items:   allItems,
		Updated: time.Now(),
	}
	jsonBytes, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON 序列化失败: %v", err))
		return
	}

	// 5. 上传到 COS
	err = uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v", err))
		return
	}

	// 6. 写日志
	if len(failedList) > 0 {
		sb := strings.Builder{}
		sb.WriteString("[WARN] 以下订阅拉取失败：\n")
		for _, f := range failedList {
			sb.WriteString(" - " + f + "\n")
		}
		_ = appendLog(ctx, sb.String())
	} else {
		_ = appendLog(ctx, "[INFO] 本次执行成功，没有失败的订阅。")
	}
}

/* -------------------- 工具函数: fetchRSSLinks / uploadToCos -------------------- */

// fetchRSSLinks 从文本文件逐行读取 RSS 链接
func fetchRSSLinks(rssListURL string) ([]string, error) {
	resp, err := http.Get(rssListURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

// uploadToCos 使用 cos-go-sdk-v5，将 data.json 覆盖上传
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
	u, err := url.Parse(dataURL)
	if err != nil {
		return err
	}
	baseURL := &cos.BaseURL{
		BucketURL: &url.URL{
			Scheme: u.Scheme,
			Host:   u.Host,
		},
	}
	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})
	key := strings.TrimPrefix(u.Path, "/")

	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	return err
}
