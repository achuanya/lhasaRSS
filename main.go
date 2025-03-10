package main

import (
	// -------------------- 引入所需包 --------------------
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
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/tencentyun/cos-go-sdk-v5"
	"golang.org/x/net/html"
)

/* -------------------- 数据结构定义 -------------------- */

// Article 结构体：只保留最关键的字段
type Article struct {
	BlogName  string `json:"blog_name"` // 博客名称
	Title     string `json:"title"`     // 文章标题
	Published string `json:"published"` // 文章发布时间 (已格式化为 "09 Mar 2025")
	Link      string `json:"link"`      // 文章链接
	Avatar    string `json:"avatar"`    // 博客头像
}

// AllData 结构体：用于最终输出 JSON
type AllData struct {
	Items   []Article `json:"items"`   // 所有文章
	Updated string    `json:"updated"` // 数据更新时间（使用中文格式的字符串）
}

/* -------------------- 并发抓取所需结构体 -------------------- */

// feedResult 用于并发抓取时，保存单个 RSS feed 的抓取结果（或错误信息）
type feedResult struct {
	Article    *Article  // 抓到的最新一篇文章（可能为 nil）
	FeedLink   string    // RSS 地址
	Err        error     // 抓取过程中的错误
	ParsedTime time.Time // 解析得到的发布时间，用于后续排序
}

/* -------------------- 时间解析相关函数 -------------------- */

// parseTime 尝试用多种格式解析 RSS 中的时间字符串
func parseTime(timeStr string) (time.Time, error) {
	// 定义可能出现的多种时间格式
	formats := []string{
		time.RFC1123Z,                     // "Mon, 02 Jan 2006 15:04:05 -0700"
		time.RFC1123,                      // "Mon, 02 Jan 2006 15:04:05 MST"
		time.RFC3339,                      // "2006-01-02T15:04:05Z07:00"
		"2006-01-02T15:04:05.000Z07:00",   // 例如 "2025-02-09T13:20:27.000Z"
		"Mon, 02 Jan 2006 15:04:05 +0000", // 有些 RSS 也可能是这种写法
	}

	// 依次尝试解析
	for _, f := range formats {
		if t, err := time.Parse(f, timeStr); err == nil {
			return t, nil
		}
	}
	// 如果都失败，就返回错误
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

/* -------------------- 头像处理相关函数 -------------------- */

// getFeedAvatarURL 尝试从 feed.Image 或者博客主页获取头像地址
func getFeedAvatarURL(feed *gofeed.Feed) string {
	// 如果 RSS 中存在 <image> 标签且 URL 不为空，则优先使用
	if feed.Image != nil && feed.Image.URL != "" {
		return feed.Image.URL
	}
	// 否则，如果 feed.Link 不为空，就尝试访问该链接获取头像
	if feed.Link != "" {
		return fetchBlogLogo(feed.Link)
	}
	// 如果以上都不行，就返回空字符串，后续再做默认头像处理
	return ""
}

// fetchBlogLogo 尝试抓取博客主页的 HTML，并从 <head> 中获取最常见的 icon；若没有则 fallback 到 favicon.ico
func fetchBlogLogo(blogURL string) string {
	// 1. 请求博客主页
	resp, err := http.Get(blogURL)
	if err != nil {
		// 如果请求失败，直接退回到 fallbackFavicon
		return fallbackFavicon(blogURL)
	}
	// 使用完毕后关闭 Body
	defer resp.Body.Close()

	// 如果响应状态不是 200，则也使用 fallback
	if resp.StatusCode != 200 {
		return fallbackFavicon(blogURL)
	}

	// 2. 解析 HTML，寻找 <link rel="icon"> / <link rel="shortcut icon"> / <link rel="apple-touch-icon"> / <meta property="og:image">
	doc, err := html.Parse(resp.Body)
	if err != nil {
		// HTML 解析失败就 fallback
		return fallbackFavicon(blogURL)
	}

	// 用于存储解析到的 icon 和 og:image
	var iconHref string
	var ogImage string

	// 递归函数，遍历整棵 DOM 树
	var f func(*html.Node)
	f = func(n *html.Node) {
		// 如果节点类型是元素节点，才考虑检查它的属性
		if n.Type == html.ElementNode {
			tagName := strings.ToLower(n.Data)
			// 处理 <link ...> 标签
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
				// 如果 rel 中包含 "icon" 就认为它是网站图标
				if strings.Contains(relVal, "icon") && hrefVal != "" {
					// 只记录第一个匹配到的 icon
					if iconHref == "" {
						iconHref = hrefVal
					}
				}
			} else if tagName == "meta" {
				// 处理 <meta ...> 标签
				var propVal, contentVal string
				for _, attr := range n.Attr {
					switch strings.ToLower(attr.Key) {
					case "property":
						propVal = strings.ToLower(attr.Val)
					case "content":
						contentVal = attr.Val
					}
				}
				// 如果是 <meta property="og:image" content="...">
				if propVal == "og:image" && contentVal != "" {
					ogImage = contentVal
				}
			}
		}
		// 继续遍历子节点
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	// 开始递归遍历
	f(doc)

	// 优先返回找到的 link rel="icon" 之类的
	if iconHref != "" {
		return makeAbsoluteURL(blogURL, iconHref)
	}
	// 其次如果有 og:image 就用之
	if ogImage != "" {
		return makeAbsoluteURL(blogURL, ogImage)
	}
	// 如果都没有，就 fallback 到 /favicon.ico
	return fallbackFavicon(blogURL)
}

// fallbackFavicon 解析出域名，然后返回 "scheme://host/favicon.ico"
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

// checkURLAvailable 通过 HEAD 请求检查某个 URL 是否可以正常访问(返回 200)
func checkURLAvailable(urlStr string) (bool, error) {
	// 创建一个自定义客户端，设置超时可避免长时间阻塞
	client := &http.Client{
		Timeout: 5 * time.Second, // 自行调整超时时间
	}
	// 构造 HEAD 请求
	req, err := http.NewRequest("HEAD", urlStr, nil)
	if err != nil {
		return false, err
	}
	// 执行请求
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	// 检查是否是 200 状态码
	return (resp.StatusCode == http.StatusOK), nil
}

/* -------------------- GitHub 日志写入相关函数 -------------------- */

// getGitHubFileSHA 获取指定仓库内某个路径文件的 SHA；若文件不存在则返回空
func getGitHubFileSHA(ctx context.Context, token, owner, repo, path string) (string, error) {
	// 拼接 GitHub API 路径
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	// 构造 GET 请求
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	// 设置授权头
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// 如果返回 404，表示文件不存在，直接返回空
	if resp.StatusCode == 404 {
		return "", nil
	}
	// 如果返回不是 200，表示失败
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	// 解析返回 JSON，获取文件 SHA
	var response struct {
		SHA string `json:"sha"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	return response.SHA, nil
}

// getGitHubFileContent 获取指定文件的完整内容和 SHA
func getGitHubFileContent(ctx context.Context, token, owner, repo, path string) (string, string, error) {
	// 拼接 GitHub API 路径
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	// 构造 GET 请求
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", "", err
	}
	// 设置授权头
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	// 404 表示文件不存在
	if resp.StatusCode == 404 {
		return "", "", nil
	}
	// 如果不是 200，表示失败
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	// 解析返回 JSON
	var response struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", "", err
	}

	// Content 是 base64 编码过的，需要解码
	decoded, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return "", "", err
	}
	return string(decoded), response.SHA, nil
}

// putGitHubFile 创建或更新 GitHub 仓库内的文件
func putGitHubFile(ctx context.Context,
	token, owner, repo, path, sha, content, commitMsg, committerName, committerEmail string) error {

	// GitHub API 地址
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	// 将文件内容再次用 base64 编码
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	// 构造提交信息
	payload := map[string]interface{}{
		"message": commitMsg,
		"content": encoded,
		"branch":  "main", // 如果仓库主分支是 master，请改成 "master"
		"committer": map[string]string{
			"name":  committerName,
			"email": committerEmail,
		},
	}
	// 如果文件已存在，需要带上旧的 sha
	if sha != "" {
		payload["sha"] = sha
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 构造 PUT 请求
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

	// GitHub 更新/创建文件成功返回 200 或 201
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to put file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// deleteGitHubFile 删除 GitHub 仓库内的文件
func deleteGitHubFile(ctx context.Context, token, owner, repo, path, sha, committerName, committerEmail string) error {
	// GitHub API 地址
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	// 构造删除所需的 JSON
	payload := map[string]interface{}{
		"message":   "Delete old log file",
		"sha":       sha,
		"branch":    "main",
		"committer": map[string]string{"name": committerName, "email": committerEmail},
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	// 构造 DELETE 请求
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

	// 删除成功返回 200
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// listGitHubDir 列出 GitHub 仓库某目录下的文件与信息
func listGitHubDir(ctx context.Context, token, owner, repo, dir string) ([]struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
	Type string `json:"type"`
}, error) {
	// GitHub API 地址
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, dir)
	// 构造 GET 请求
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

	// 如果 404，说明目录不存在，返回空列表
	if resp.StatusCode == 404 {
		return nil, nil
	}
	// 如果不是 200，说明出现错误
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list dir %s, status: %d, body: %s",
			dir, resp.StatusCode, string(bodyBytes))
	}

	// 解析 JSON，返回文件列表
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
appendLog 函数：用于将日志内容写入 GitHub 仓库的 logs/YYYY-MM-DD.log 文件
同时会自动清理 7 天前的老日志
*/
func appendLog(ctx context.Context, rawLogContent string) error {
	// 从环境变量中读取必要参数
	token := os.Getenv("TOKEN")         // GitHub Token
	githubUser := os.Getenv("NAME")     // GitHub 用户名
	repoName := os.Getenv("REPOSITORY") // 仓库名
	owner := githubUser                 // 仓库 Owner
	repo := repoName                    // 仓库名

	// 提交时的 committer 信息
	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	// 拼出当日日志文件路径，例如 logs/2025-03-10.log
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join("logs", dateStr+".log")

	// 1. 先获取旧内容(如果有)
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 2. 生成带时间戳的新日志段
	var sb strings.Builder
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	lines := strings.Split(rawLogContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 每行前面加上时间戳
		sb.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, line))
	}
	newLogSegment := sb.String()

	// 拼接旧内容和新内容
	newContent := oldContent + newLogSegment

	// 3. 上传更新到 GitHub
	err = putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent,
		"Update log: "+dateStr, committerName, committerEmail)
	if err != nil {
		return err
	}

	// 4. 清理 7 天前的日志文件
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除 7 天前的日志文件，避免仓库过于臃肿
func cleanOldLogs(ctx context.Context, token, owner, repo, committerName, committerEmail string) error {
	// 列出 logs 目录下所有文件
	files, err := listGitHubDir(ctx, token, owner, repo, "logs")
	if err != nil {
		// 如果出错就放弃清理，但不阻塞整个流程
		return nil
	}
	// 获取 7 天前的时间
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)

	// 遍历日志文件，如果是 7 天前则删除
	for _, f := range files {
		// 只处理文件类型
		if f.Type != "file" {
			continue
		}
		// 匹配形如 YYYY-MM-DD.log 的文件名
		matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}\.log$`, f.Name)
		if !matched {
			continue
		}
		// 取出日期部分
		dateStr := strings.TrimSuffix(f.Name, ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		// 若这个日期在 7 天前
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

/* -------------------- 工具函数: fetchRSSLinks / uploadToCos -------------------- */

// fetchRSSLinks 从给定 URL 的文本文件逐行读取 RSS 链接
func fetchRSSLinks(rssListURL string) ([]string, error) {
	// 直接 GET 请求文本文件
	resp, err := http.Get(rssListURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 如果不是 200 则报错
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	// 读取响应 Body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 按行切分，并清洗空格
	var links []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	return links, nil
}

// uploadToCos 使用 cos-go-sdk-v5，将 data.json 覆盖上传到对应 Bucket
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
	// 解析 dataURL
	u, err := url.Parse(dataURL)
	if err != nil {
		return err
	}
	// 构造 COS 客户端所需的 BaseURL
	baseURL := &cos.BaseURL{
		BucketURL: &url.URL{
			Scheme: u.Scheme,
			Host:   u.Host,
		},
	}
	// 实例化 COS 客户端
	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})
	// 去掉路径前面的斜杠
	key := strings.TrimPrefix(u.Path, "/")

	// 执行 Put 上传
	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	return err
}

/* -------------------- 主函数：并发抓取 & 按日期排序 & 头像校验 & 上传 -------------------- */

func main() {
	// 创建一个上下文，可在整个流程中使用
	ctx := context.Background()

	// 读取环境变量
	secretID := os.Getenv("TENCENT_CLOUD_SECRET_ID")   // 腾讯云 COS 的 secretID
	secretKey := os.Getenv("TENCENT_CLOUD_SECRET_KEY") // 腾讯云 COS 的 secretKey
	rssListURL := os.Getenv("RSS")                     // RSS 列表文件的 URL
	dataURL := os.Getenv("DATA")                       // data.json 要上传到的 COS URL
	defaultAvatar := os.Getenv("DEFAULT_AVATAR")       // 没有或无法访问头像时的默认头像

	// 简单调试输出
	fmt.Println(">> Debug: secretID=", secretID)
	fmt.Println(">> Debug: secretKey=", secretKey)
	fmt.Println(">> Debug: RSS=", rssListURL)
	fmt.Println(">> Debug: DATA=", dataURL)
	fmt.Println(">> Debug: TOKEN=", os.Getenv("TOKEN"))
	fmt.Println(">> Debug: NAME=", os.Getenv("NAME"))
	fmt.Println(">> Debug: REPOSITORY=", os.Getenv("REPOSITORY"))
	fmt.Println(">> Debug: DEFAULT_AVATAR=", defaultAvatar)

	// 如果基础环境变量不全，直接写日志并退出
	if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
		_ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置。")
		return
	}
	if defaultAvatar == "" {
		_ = appendLog(ctx, "[WARN] 未设置 DEFAULT_AVATAR，将可能导致没有头像时出现空字符串。")
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

	// 并发解析：限制并发数量，避免同时抓取过多导致阻塞/超时
	maxGoroutines := 10 // 可根据实际情况调整并发数
	sem := make(chan struct{}, maxGoroutines)

	// 使用 WaitGroup 等待所有并发完成
	var wg sync.WaitGroup

	// 建立一个 channel 收集结果
	resultChan := make(chan feedResult, len(rssLinks))

	// 实例化一个 RSS 解析器
	fp := gofeed.NewParser()

	// 2. 并发抓取每个 RSS 链接，只抓最新文章
	for _, link := range rssLinks {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{} // 占用一个并发槽

		go func(rssLink string) {
			defer wg.Done()
			defer func() { <-sem }() // 任务结束后释放一个并发槽

			// 创建一个结果对象
			fr := feedResult{FeedLink: rssLink}

			// 执行 RSS 解析
			feed, err := fp.ParseURL(rssLink)
			if err != nil {
				fr.Err = fmt.Errorf("订阅链接: %s，错误: %v", rssLink, err)
				resultChan <- fr
				return
			}
			if feed == nil || len(feed.Items) == 0 {
				// feed.Items 为空，直接返回错误
				fr.Err = fmt.Errorf("订阅链接: %s 没有任何文章", rssLink)
				resultChan <- fr
				return
			}

			// 获取头像 URL
			avatarURL := getFeedAvatarURL(feed)

			// 如果头像 URL 为空，直接使用默认头像
			// 否则校验可否访问，不可访问也使用默认头像
			if avatarURL == "" {
				avatarURL = defaultAvatar
			} else {
				ok, _ := checkURLAvailable(avatarURL)
				if !ok {
					avatarURL = defaultAvatar
				}
			}

			// 只取最新的一篇文章
			latest := feed.Items[0]

			// 尝试解析发布时间；如果失败，就标记为当前时间（或其他默认值）
			pubTime, err := time.Now(), error(nil)
			if latest.PublishedParsed != nil {
				// 有些 RSS 解析器会自动解析出 time.Time
				pubTime = *latest.PublishedParsed
			} else if latest.Published != "" {
				// 如果 PublishedParsed 为空，则自己来解析
				if t, e := parseTime(latest.Published); e == nil {
					pubTime = t
				}
			}

			// 将解析到的时间存入 fr，供后续排序使用
			fr.ParsedTime = pubTime

			// 格式化成 "09 Mar 2025"
			formattedPub := pubTime.Format("02 Jan 2006")

			// 构造 Article 实例
			fr.Article = &Article{
				BlogName:  feed.Title,
				Title:     latest.Title,
				Published: formattedPub, // 使用格式化后的发布时间字符串
				Link:      latest.Link,
				Avatar:    avatarURL,
			}
			// 将结果发送回主 goroutine
			resultChan <- fr
		}(link)
	}

	// 3. 等待所有 RSS 抓取结束，关闭通道
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// 用来存放所有成功抓取的 Article
	var allItems []Article
	// 额外存储 (Article, 真实发布时间) 供排序
	type itemWithTime struct {
		article Article
		t       time.Time
	}
	var itemsWithTime []itemWithTime

	// 收集抓取失败的信息
	var failedList []string

	// 逐条读取结果
	for r := range resultChan {
		// 如果出错，则把错误累加到 failedList
		if r.Err != nil {
			failedList = append(failedList, r.Err.Error())
			continue
		}
		// 否则就把成功抓到的 article 加到 allItems
		if r.Article != nil {
			itemsWithTime = append(itemsWithTime, itemWithTime{
				article: *r.Article,
				t:       r.ParsedTime,
			})
		}
	}

	// 4. 按发布时间升序（越早的排前）排序
	sort.Slice(itemsWithTime, func(i, j int) bool {
		return itemsWithTime[i].t.Before(itemsWithTime[j].t)
	})

	// 排序后再赋值回 allItems
	for _, v := range itemsWithTime {
		allItems = append(allItems, v.article)
	}

	// 5. 组装最终输出的 JSON
	allData := AllData{
		Items: allItems,
		// 使用本地时间并格式化成中文方式
		Updated: time.Now().Local().Format("2006年01月02日 15:04:05"),
	}

	jsonBytes, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON 序列化失败: %v", err))
		return
	}

	// 6. 上传到 COS
	err = uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v", err))
		return
	}

	// 7. 写执行日志，记录成功或失败信息
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
