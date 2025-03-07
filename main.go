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

// Article 表示单篇文章（这里只保留最关键字段）
type Article struct {
	BlogName  string `json:"blog_name"`
	Title     string `json:"title"`
	Published string `json:"published"`
	Link      string `json:"link"`
	Avatar    string `json:"avatar"` // 新增: 博客头像链接
}

// AllData 用于整体数据结构
type AllData struct {
	Items   []Article `json:"items"`
	Updated time.Time `json:"updated"`
}

/* -------------------- 这部分与 GitHub 日志读写相关的代码保持不变 -------------------- */

// getGitHubFileSHA 获取指定文件的 SHA（如果文件不存在则返回空字符串）
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
		// 文件不存在
		return "", nil
	}
	if resp.StatusCode != 200 {
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

	decoded, err := base64.StdEncoding.DecodeString(response.Content)
	if err != nil {
		return "", "", err
	}
	return string(decoded), response.SHA, nil
}

// putGitHubFile 往仓库写文件(创建或覆盖)
func putGitHubFile(ctx context.Context, token, owner, repo, path, sha, content, commitMsg, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	payload := map[string]interface{}{
		"message": commitMsg,
		"content": encoded,
		"branch":  "main", // 如果你的仓库默认分支是 master，则改成 "master"
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

// deleteGitHubFile 删除指定文件(如过期日志)
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

// listGitHubDir 列出仓库中某个目录下的文件
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
	repoName := os.Getenv("REPOSITORY") // 仓库名
	owner := githubUser
	repo := repoName

	// 如果 REPOSITORY 只包含仓库名，则拼接成 owner/repo
	// if !strings.Contains(repoName, "/") {
	// 	repo = owner + "/" + repoName
	// }

	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	dateStr := time.Now().Format("2006-01-02") // e.g. 2025-03-07
	logPath := filepath.Join("logs", dateStr+".log")

	// 1. 获取旧日志内容
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 2. 拼接新的日志
	newContent := oldContent + logContent

	// 3. 上传/更新 GitHub
	err = putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent,
		"Update log: "+dateStr, committerName, committerEmail)
	if err != nil {
		return err
	}

	// 4. 删除 7 天前的日志
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除 7 天前的日志文件
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

/* -------------------- 主函数：只抓取每个源的最新1篇 + 提取头像链接 -------------------- */

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

	// 简单判断必要变量是否存在
	if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
		_ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置。\n")
		return
	}

	// 1. 拉取包含 RSS 链接的文件
	rssLinks, err := fetchRSSLinks(rssListURL)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取 RSS 列表文件失败: %v\n", err))
		return
	}
	if len(rssLinks) == 0 {
		_ = appendLog(ctx, "[WARN] RSS 列表为空。\n")
		return
	}

	// 2. 逐个 RSS 订阅，只抓取**最新一篇**文章 + 提取头像
	fp := gofeed.NewParser()
	var allItems []Article
	var failedList []string

	for _, link := range rssLinks {
		if link == "" {
			continue
		}
		feed, err := fp.ParseURL(link)
		if err != nil {
			// 记录出错的订阅链接
			failedList = append(failedList, fmt.Sprintf("订阅链接: %s，错误: %v", link, err))
			continue
		}

		if feed == nil || len(feed.Items) == 0 {
			// 如果这个订阅源没有文章，就略过
			continue
		}

		// 获取博客头像
		avatarURL := ""
		if feed.Image != nil {
			avatarURL = feed.Image.URL
		}

		// 只取最新的一篇：假设 feed.Items[0] 就是最新
		// 如果想更严谨，可以对 feed.Items 做一次排序找出发布日期最晚的一篇
		latestItem := feed.Items[0]

		article := Article{
			BlogName:  feed.Title,
			Title:     latestItem.Title,
			Published: latestItem.Published,
			Link:      latestItem.Link,
			Avatar:    avatarURL,
		}
		allItems = append(allItems, article)
	}

	// 3. 若希望所有抓到的“最新文章”也按发布时间排序，可在这里统一排一下：
	sort.Slice(allItems, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC1123Z, allItems[i].Published)
		tj, _ := time.Parse(time.RFC1123Z, allItems[j].Published)
		return ti.After(tj)
	})

	// 4. 组装整体数据并序列化
	allData := AllData{
		Items:   allItems,
		Updated: time.Now(),
	}
	jsonBytes, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON 序列化失败: %v\n", err))
		return
	}

	// 5. 上传到 COS
	err = uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v\n", err))
		return
	}

	// 6. 如果有失败的订阅，写入警告；否则写成功信息
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

/* -------------------- 辅助函数：拉取 RSS 列表、上传到 COS -------------------- */

// fetchRSSLinks 从给定 URL 拉取文本文件，逐行读取并去除空行，返回 RSS 链接切片
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

	// 路径去掉最前面的斜杠
	key := strings.TrimPrefix(u.Path, "/")

	// PutObject 方式覆盖上传
	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	return err
}
