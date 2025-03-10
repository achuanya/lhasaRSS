// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: logger.go
// 说明: 包含与GitHub日志写入和清理旧日志相关的功能

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// appendLog 函数: 将日志内容追加到 GitHub 仓库中的某一天的日志文件里
// 游钓四方 <haibao1027@gmail.com>
// 参数:
//   - ctx: 上下文 context, 用于控制请求超时、取消等
//   - rawLogContent: 原始的日志字符串
//
// 返回:
//   - error: 如果写入或交互出现错误, 则返回错误; 否则返回nil
//
// 作用:
//   - 每次执行时, 都将日志追加到 logs/日期.log 文件中
//   - 并且会顺带删除 7 天前的日志文件 (cleanOldLogs)
func appendLog(ctx context.Context, rawLogContent string) error {
	// 从环境变量获取信息
	token := os.Getenv("TOKEN")
	githubUser := os.Getenv("NAME")
	repoName := os.Getenv("REPOSITORY")
	owner := githubUser
	repo := repoName

	committerName := githubUser
	committerEmail := githubUser + "@users.noreply.github.com"

	// 日志文件名: logs/2025-03-10.log
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join("logs", dateStr+".log")

	// 获取旧日志内容(如果有), 以及旧日志文件的SHA
	oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
	if err != nil {
		return err
	}

	// 构造新的日志段落, 每行前加上时间戳
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

	// 上传新内容到GitHub
	err = putGitHubFile(
		ctx,
		token,
		owner,
		repo,
		logPath,
		oldSHA,
		newContent,
		"Update log: "+dateStr,
		committerName,
		committerEmail,
	)
	if err != nil {
		return err
	}

	// 清理7天前的日志
	return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除7天前的日志文件
// 游钓四方 <haibao1027@gmail.com>
// 参数:
//   - ctx: 上下文 context, 用于控制请求的取消或超时
//   - token: GitHub的Token
//   - owner: 仓库所有者
//   - repo: 仓库名
//   - committerName: 提交者名称
//   - committerEmail: 提交者邮箱
//
// 返回:
//   - error: 如果清理过程出现错误则返回错误, 否则返回nil
//
// 作用:
//   - 遍历 logs 目录下的文件, 如果文件日期是7天前, 则删除
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
		// 判断文件名形如 "2025-03-10.log"
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

// getGitHubFileContent 获取指定文件的完整内容和 SHA
// 游钓四方 <haibao1027@gmail.com>
// 返回: (内容, SHA, error)
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
		return "", "", fmt.Errorf("failed to get file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}

	var response struct {
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", "", err
	}

	decoded, err := decodeBase64(response.Content)
	if err != nil {
		return "", "", err
	}
	return decoded, response.SHA, nil
}
