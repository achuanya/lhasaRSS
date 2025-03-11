// Author: 游钓四方 <haibao1027@gmail.com>
// File: logger.go
// Description: 包含与GitHub日志写入和清理旧日志相关的功能

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// appendLog 将日志内容追加到 GitHub 仓库中的某一天的日志文件里
//
// Description:
//
//	每次调用本函数，会将传入的 rawLogContent（原始日志）按行加上时间戳后，
//	追加写入到当日日期命名的日志文件： logs/2025-03-10.log
//	若日志文件不存在，会自动创建
//	同时会调用 cleanOldLogs 清理 7 天之前的日志文件
//
// Parameters:
//   - ctx           : 上下文 context，用于控制请求超时、取消等
//   - rawLogContent : 原始的日志字符串
//
// Returns:
//   - error: 如果写入或网络交互出现错误，则返回错误；否则返回nil
func appendLog(ctx context.Context, rawLogContent string) error {
	// 从 config.go 中加载统一的环境变量配置
	cfg := LoadConfig()

	// 提交者信息
	committerName := cfg.GitHubName
	committerEmail := cfg.GitHubName + "@users.noreply.github.com"

	// 生成日志文件名，例如：logs/2025-03-10.log
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join("logs", dateStr+".log")

	// 先获取旧日志内容和旧日志文件的SHA
	oldContent, oldSHA, err := getGitHubFileContent(ctx, cfg.GitHubToken, cfg.GitHubName, cfg.GitHubRepo, logPath)
	if err != nil {
		return err
	}

	// 构造新的日志段落，将 rawLogContent 每一行都加上当前时间戳
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

	// 拼接到旧日志内容上
	newContent := oldContent + newLogSegment

	// 将拼接后的完整日志上传到GitHub
	err = putGitHubFile(
		ctx,
		cfg.GitHubToken,
		cfg.GitHubName,
		cfg.GitHubRepo,
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
	return cleanOldLogs(ctx)
}

// cleanOldLogs 删除7天前的日志文件
//
// Description:
//
//	遍历 logs 目录下的所有文件，检查文件名是否符合 "YYYY-MM-DD.log" 的日期格式，
//	若其日期早于7天前，则删除
//
// Parameters:
//   - ctx            : 上下文 context
//   - token          : GitHub Token
//   - owner          : 仓库所有者
//   - repo           : 仓库名
//   - committerName  : 提交者姓名
//   - committerEmail : 提交者邮箱
//
// Returns:
//   - error: 如果删除过程中出现错误则返回，否则返回nil
func cleanOldLogs(ctx context.Context) error {
	cfg := LoadConfig()

	committerName := cfg.GitHubName
	committerEmail := cfg.GitHubName + "@users.noreply.github.com"

	// 列出 logs 目录下的所有文件或子目录
	files, err := listGitHubDir(ctx, cfg.GitHubToken, cfg.GitHubName, cfg.GitHubRepo, "logs")
	if err != nil {
		return nil
	}

	sevenDaysAgo := time.Now().AddDate(0, 0, -7)

	for _, f := range files {
		// 如果不是文件，则跳过
		if f.Type != "file" {
			continue
		}

		// 文件名形如 "2025-03-10.log" 才可能是日志文件
		matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}\.log$`, f.Name)
		if !matched {
			continue
		}

		// 解析文件名中的日期
		dateStr := strings.TrimSuffix(f.Name, ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		// 如果该日志的日期早于7天前，则删除
		if t.Before(sevenDaysAgo) {
			path := filepath.Join("logs", f.Name)
			delErr := deleteGitHubFile(
				ctx,
				cfg.GitHubToken,
				cfg.GitHubName,
				cfg.GitHubRepo,
				path,
				f.SHA,
				committerName,
				committerEmail,
			)
			if delErr != nil {
				fmt.Printf("删除旧日志 %s 失败: %v\n", f.Name, delErr)
			} else {
				fmt.Printf("已删除旧日志 %s\n", f.Name)
			}
		}
	}
	return nil
}

// getGitHubFileContent 获取指定文件的完整内容和SHA
//
// Description:
//
//	通过 GitHub API 获取远程文件的内容（base64 编码），然后进行解码，
//	并同时获取其 SHA 值用于后续更新或删除操作
//	如果文件不存在（404），则返回空内容、空SHA
//
// Parameters:
//   - ctx   : 上下文
//   - token : GitHub Token
//   - owner : 仓库所有者
//   - repo  : 仓库名
//   - path  : 文件路径
//
// Returns:
//   - string: 文件的解码后内容
//   - string: 文件的SHA
//   - error : 若出现请求或解码错误，则返回
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
		// 文件不存在
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
