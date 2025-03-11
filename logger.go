// Author: 游钓四方 <haibao1027@gmail.com>
// File: logger.go
// Description: 包含与GitHub日志写入和清理旧日志相关的功能

package main

import (
	"context"
	"fmt"
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
func appendLog(ctx context.Context, rawLogContent string) error {
	cfg := LoadConfig()

	committerName := cfg.GitHubName
	committerEmail := cfg.GitHubName + "@users.noreply.github.com"

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
func cleanOldLogs(ctx context.Context) error {
	cfg := LoadConfig()

	committerName := cfg.GitHubName
	committerEmail := cfg.GitHubName + "@users.noreply.github.com"

	files, err := listGitHubDir(ctx, cfg.GitHubToken, cfg.GitHubName, cfg.GitHubRepo, "logs")
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

// summarizeResults 根据抓取成功数、总数和问题信息, 生成日志字符串
//
// Description:
//
//	将本次抓取的结果进行简单的统计说明，包含解析失败数量、空RSS数量、
//	头像缺失或不可用的数量等，并以字符串形式返回，便于写日志
//
// Parameters:
//   - successCount : 成功抓取的数量
//   - total        : 总RSS链接数量
//   - problems     : 各种问题的集合（parseFails, feedEmpties, noAvatar, brokenAvatar）
//
// Returns:
//   - string: 整理好的日志数据
func summarizeResults(successCount, total int, problems map[string][]string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("本次订阅抓取结果统计:\n"))
	sb.WriteString(fmt.Sprintf("共 %d 条RSS, 成功抓取 %d 条.\n", total, successCount))

	parseFails := problems["parseFails"]
	if len(parseFails) > 0 {
		sb.WriteString(fmt.Sprintf("✘ 有 %d 条订阅解析失败:\n", len(parseFails)))
		for _, l := range parseFails {
			sb.WriteString("  - " + l + "\n")
		}
	}

	feedEmpties := problems["feedEmpties"]
	if len(feedEmpties) > 0 {
		sb.WriteString(fmt.Sprintf("✘ 有 %d 条订阅为空:\n", len(feedEmpties)))
		for _, l := range feedEmpties {
			sb.WriteString("  - " + l + "\n")
		}
	}

	noAvatarList := problems["noAvatar"]
	if len(noAvatarList) > 0 {
		sb.WriteString(fmt.Sprintf("✘ 有 %d 条订阅头像字段为空, 已使用默认头像:\n", len(noAvatarList)))
		for _, l := range noAvatarList {
			sb.WriteString("  - " + l + "\n")
		}
	}

	brokenAvatarList := problems["brokenAvatar"]
	if len(brokenAvatarList) > 0 {
		sb.WriteString(fmt.Sprintf("✘ 有 %d 条订阅头像无法访问, 已使用默认头像:\n", len(brokenAvatarList)))
		for _, l := range brokenAvatarList {
			sb.WriteString("  - " + l + "\n")
		}
	}

	if len(parseFails) == 0 && len(feedEmpties) == 0 && len(noAvatarList) == 0 && len(brokenAvatarList) == 0 {
		sb.WriteString("没有任何警告或错误, 一切正常\n")
	}
	return sb.String()
}
