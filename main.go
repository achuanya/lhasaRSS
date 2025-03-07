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
)

// Article 表示单篇文章（只保留关键字段）
type Article struct {
	BlogName  string `json:"blog_name"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Link      string `json:"link"`
	Avatar    string `json:"avatar"`
}

// AllData 整体数据结构
type AllData struct {
	Items   []Article `json:"items"`
	Updated time.Time `json:"updated"`
}

// fetchFeedAvatarURL 是我们“多次尝试抓取头像”的核心函数
func fetchFeedAvatarURL(feed *gofeed.Feed) string {
	// 1）若 feed.Image 存在并且有 URL，就用这个
	if feed.Image != nil && feed.Image.URL != "" {
		return feed.Image.URL
	}

	// 2）若有 iTunes 信息，则尝试 feed.ITunes.Image
	// gofeed 对 iTunes 的字段支持见 https://pkg.go.dev/github.com/mmcdole/gofeed#ITunes
	if feed.ITunes != nil && feed.ITunes.Image != "" {
		return feed.ITunes.Image
	}

	// 3）如果都没有，则退化到「domain + /favicon.ico」
	if feed.Link != "" {
		u, err := url.Parse(feed.Link)
		if err == nil && u.Scheme != "" && u.Host != "" {
			return fmt.Sprintf("%s://%s/favicon.ico", u.Scheme, u.Host)
		}
	}

	// 4）完全找不到，就返回空
	return ""
}

/*
-------------------- GitHub 相关日志读写函数（和原先类似） --------------------
*/

// getGitHubFileSHA 获取指定文件的 SHA（若文件不存在则返回空）
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

// getGitHubFileContent 获取 GitHub 上文件（解码后），不存在则返回空
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

// putGitHubFile 创建/更新文件
func putGitHubFile(ctx context.Context, token, owner, repo, path, sha, content, commitMsg, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	payload := map[string]interface{}{
		"message": commitMsg,
		"content": encoded,
		"branch":  "main", // 如果仓库分支是 master，则改为 master
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

// deleteGitHubFile 删除文件(如旧日志)
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

// listGitHubDir 列出目录
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
	if err = json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

/*
appendLog: 在旧日志基础上追加新内容。这里把「给日志加上时间戳」也写进去了。

思路：
- 先获取今天的日志文件
- 为要写的内容的每一行，加上 "[YYYY-MM-DD HH:MM:SS]" 前缀
- 追加到旧日志
- 再上传到仓库
*/
func appendLog(ctx context.Context, rawLogContent string) error {
	token := os.Getenv("TOKEN")
	githubUser := os.Getenv("NAME")
	repoName := os.Getenv("REPOSITORY")

	owner := githubUser
	repo := repoName
	// if !strings.Contains(repoName, "/") {
	// 	repo = owner + "/" + repoName
	// }

	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	dateStr := time.Now().Format("2006-01-02") // 如 "2025-03-07"
	logPath := filepath.Join("logs", dateStr+".log")

	// 1. 读取旧日志
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 2. 为新日志的每一行都加上带时间的前缀
	var sb strings.Builder
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	lines := strings.Split(rawLogContent, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 每一行前加上 "[2025-03-07 13:45:01]" 这样的时间
		sb.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, line))
	}
	newLogSegment := sb.String()

	// 3. 拼接
	newContent := oldContent + newLogSegment

	// 4. 上传更新
	if err := putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent,
		"Update log: "+dateStr, committerName, committerEmail); err != nil {
		return err
	}

	// 5. 清理 7 天前的日志
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除7天前日志
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
			logFullPath := filepath.Join("logs", f.Name)
			delErr := deleteGitHubFile(ctx, token, owner, repo, logFullPath, f.SHA, committerName, committerEmail)
			if delErr != nil {
				fmt.Printf("删除旧日志 %s 失败: %v\n", f.Name, delErr)
			} else {
				fmt.Printf("已删除旧日志 %s\n", f.Name)
			}
		}
	}
	return nil
}

/* -------------------- 主函数：抓最新一篇文章 & 高级头像 & 记录日志 -------------------- */

func main() {
	ctx := context.Background()

	// 读取环境变量
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
		_ = appendLog(ctx, "[ERROR] 必要的环境变量缺失, 请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA.")
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

	// 2. 逐个解析，只取最新文章，获取头像
	fp := gofeed.NewParser()
	var allItems []Article
	var failedList []string

	for _, link := range rssLinks {
		if link == "" {
			continue
		}
		feed, err := fp.ParseURL(link)
		if err != nil {
			failedList = append(failedList, fmt.Sprintf("订阅链接: %s，错误: %v", link, err))
			continue
		}
		if feed == nil || len(feed.Items) == 0 {
			continue
		}

		avatar := fetchFeedAvatarURL(feed)

		// 只取最新一篇 (feed.Items[0])，如果想更严谨可以先排序
		latest := feed.Items[0]
		newArticle := Article{
			BlogName:  feed.Title,
			Title:     latest.Title,
			Published: latest.Published,
			Link:      latest.Link,
			Avatar:    avatar,
		}
		allItems = append(allItems, newArticle)
	}

	// 3. 若需要对最终结果再按发布时间排序，可执行以下逻辑
	sort.Slice(allItems, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC1123Z, allItems[i].Published)
		tj, _ := time.Parse(time.RFC1123Z, allItems[j].Published)
		return ti.After(tj)
	})

	// 4. 组装 JSON 数据
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

	// 6. 日志：如果有失败订阅就写 [WARN]，否则 [INFO]
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

/* -------------------- fetchRSSLinks & uploadToCos 保持与原先类似 -------------------- */

// fetchRSSLinks 拉取文本文件，按行分割为 RSS 链接
func fetchRSSLinks(rssListURL string) ([]string, error) {
	resp, err := http.Get(rssListURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status code: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(body), "\n")
	var links []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			links = append(links, line)
		}
	}
	return links, nil
}

// uploadToCos 使用 cos-go-sdk-v5，将数据覆盖上传到指定的 data.json
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
