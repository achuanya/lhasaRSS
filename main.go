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

// Article 用于存储单篇文章
type Article struct {
	BlogName  string `json:"blog_name"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Content   string `json:"content"`
}

// AllData 用于整体数据结构
type AllData struct {
	Items   []Article `json:"items"`
	Updated time.Time `json:"updated"`
}

// 以下函数用于请求 GitHub API 写入/读取/删除日志文件
func getGitHubFileSHA(ctx context.Context, token, owner, repo, path string) (string, error) {
	// 获取指定文件的 SHA（如果文件不存在会返回 404）
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
		// 文件不存在
		return "", nil
	}
	if resp.StatusCode != 200 {
		// 其它错误
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	var response struct {
		SHA string `json:"sha"`
	}
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return "", err
	}
	return response.SHA, nil
}

// getGitHubFileContent 获取 GitHub 上文件内容(解码后)，如果不存在则返回空字符串
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
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return "", "", err
	}

	// GitHub API 返回的 content 是 base64 编码
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
		"branch":  "main",
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

// deleteGitHubFile 用于删除过期的日志
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

// listGitHubDir 列出指定路径下的文件
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
		return nil, nil // 目录不存在
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
	err = json.NewDecoder(resp.Body).Decode(&files)
	if err != nil {
		return nil, err
	}

	return files, nil
}

// appendLog 读取今天的日志文件内容后，追加新的日志内容，再上传
func appendLog(ctx context.Context, logContent string) error {
	token := os.Getenv("TOKEN")
	githubUser := os.Getenv("NAME")     // GitHub 用户名
	repoName := os.Getenv("REPOSITORY") // 仓库名，例如 lhasaRSS
	owner := githubUser
	repo := repoName
	if !strings.Contains(repoName, "/") {
		// 若 REPOSITORY 只写了仓库名，则自动拼上 owner
		repo = owner + "/" + repoName
	}

	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	dateStr := time.Now().Format("2006-01-02") // 如 2025-03-07
	logPath := filepath.Join("logs", dateStr+".log")

	// 1. 先获取旧内容(如果有)
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 2. 拼接新的日志内容
	newContent := oldContent + logContent

	// 3. 上传(创建/更新)到 GitHub
	err = putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent, "Update log: "+dateStr, committerName, committerEmail)
	if err != nil {
		return err
	}

	// 4. 删除 7 天前的日志文件
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除 7 天前的日志文件
func cleanOldLogs(ctx context.Context, token, owner, repo, committerName, committerEmail string) error {
	files, err := listGitHubDir(ctx, token, owner, repo, "logs")
	if err != nil {
		// 如果 logs 目录都不存在，那就跳过
		return nil
	}
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	for _, f := range files {
		if f.Type != "file" {
			continue
		}
		// 日志文件名形如 2025-03-07.log
		matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}\.log$`, f.Name)
		if !matched {
			continue
		}
		dateStr := strings.TrimSuffix(f.Name, ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		// 如果文件日期早于 7 天前，则删除
		if t.Before(sevenDaysAgo) {
			// 删除
			err := deleteGitHubFile(ctx, token, owner, repo, filepath.Join("logs", f.Name), f.SHA, committerName, committerEmail)
			if err != nil {
				fmt.Printf("删除旧日志 %s 失败: %v\n", f.Name, err)
			} else {
				fmt.Printf("已删除旧日志 %s\n", f.Name)
			}
		}
	}
	return nil
}

func main() {
	ctx := context.Background()

	// 从环境变量读取
	secretID := os.Getenv("TENCENT_CLOUD_SECRET_ID")
	secretKey := os.Getenv("TENCENT_CLOUD_SECRET_KEY")
	rssListURL := os.Getenv("RSS") // 例如: https://lhasa-1253887673.cos.ap-shanghai.myqcloud.com/lhasaRSS/rss.txt
	dataURL := os.Getenv("DATA")   // 例如: https://lhasa-1253887673.cos.ap-shanghai.myqcloud.com/lhasaRSS/data.json

	if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
		_ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置。\n")
		return
	}

	// 拉取 RSS 列表文件
	rssLinks, err := fetchRSSLinks(rssListURL)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取 RSS 列表文件失败: %v\n", err))
		return
	}
	if len(rssLinks) == 0 {
		_ = appendLog(ctx, "[WARN] RSS 列表为空。\n")
		return
	}

	// 解析所有 RSS 并整理数据
	allItems := []Article{}
	var failedList []string

	fp := gofeed.NewParser()
	for _, link := range rssLinks {
		if link == "" {
			continue
		}
		feed, err := fp.ParseURL(link)
		if err != nil {
			// 记录错误
			failedList = append(failedList, fmt.Sprintf("订阅链接: %s，错误: %v", link, err))
			continue
		}
		blogName := feed.Title
		for _, item := range feed.Items {
			article := Article{
				BlogName:  blogName,
				Title:     item.Title,
				Published: fmt.Sprintf("%v", item.Published),
				Content:   item.Content,
			}
			// 若 Content 为空，则尝试使用 Description
			if article.Content == "" {
				article.Content = item.Description
			}
			allItems = append(allItems, article)
		}
	}

	// 按时间排序，让最新的文章放前面（可选）
	sort.Slice(allItems, func(i, j int) bool {
		// 若无法解析发布时间，则不交换
		ti, _ := time.Parse(time.RFC1123Z, allItems[i].Published)
		tj, _ := time.Parse(time.RFC1123Z, allItems[j].Published)
		return ti.After(tj)
	})

	allData := AllData{
		Items:   allItems,
		Updated: time.Now(),
	}
	jsonBytes, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON 序列化失败: %v\n", err))
		return
	}

	// 上传到 COS
	err = uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v\n", err))
		return
	}

	// 如果有错误的订阅链接，写入日志
	if len(failedList) > 0 {
		sb := strings.Builder{}
		sb.WriteString("[WARN] 以下订阅拉取失败：\n")
		for _, f := range failedList {
			sb.WriteString(" - " + f + "\n")
		}
		_ = appendLog(ctx, sb.String())
	} else {
		_ = appendLog(ctx, "[INFO] 本次执行成功，没有失败的订阅。\n")
	}
}

// fetchRSSLinks 从给定的 URL 拉取文件内容，并按行拆分为切片
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
	var res []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			res = append(res, line)
		}
	}
	return res, nil
}

// uploadToCos 使用 cos-go-sdk-v5，将数据覆盖上传到指定的 data.json
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
	// dataURL 形如: https://<bucket>.cos.<region>.myqcloud.com/path/to/data.json
	u, err := url.Parse(dataURL)
	if err != nil {
		return err
	}
	// BucketURL 只取 https://<bucket>.cos.<region>.myqcloud.com
	baseURL := &cos.BaseURL{BucketURL: &url.URL{
		Scheme: u.Scheme,
		Host:   u.Host,
	}}
	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})
	// 路径即为除去 host 之后的部分，如 /lhasaRSS/data.json
	key := strings.TrimPrefix(u.Path, "/")

	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	if err != nil {
		return err
	}
	return nil
}
