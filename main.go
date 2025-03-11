// Author: 游钓四方 <haibao1027@gmail.com>
// File: main.go
// Description: 程序入口文件, 读取环境变量, 并进行业务逻辑调度

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// main 程序入口
//
// Description:
//  1. 从环境变量中读取必要信息(SecretID, SecretKey, RSS, DATA, DEFAULT_AVATAR等)
//  2. 拉取RSS列表并并发抓取
//  3. 将结果整合为 data.json 并上传到指定COS路径
//  4. 写执行日志到GitHub
func main() {
	// 创建上下文，可用于取消或超时
	ctx := context.Background()

	// 读取环境变量读取配置
	secretID := os.Getenv("TENCENT_CLOUD_SECRET_ID")   // 腾讯云COS SecretID
	secretKey := os.Getenv("TENCENT_CLOUD_SECRET_KEY") // 腾讯云COS SecretKey
	rssListURL := os.Getenv("RSS")                     // RSS列表TXT在COS中的URL
	dataURL := os.Getenv("DATA")                       // data.json在COS中的URL
	defaultAvatar := os.Getenv("DEFAULT_AVATAR")       // 默认头像

	// 如果重要环境变量缺失，直接写日志并退出
	if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
		_ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置")
		return
	}
	if defaultAvatar == "" {
		_ = appendLog(ctx, "[WARN] 未设置 DEFAULT_AVATAR，将可能导致头像为空字符串")
	}

	// 拉取RSS列表
	rssLinks, err := fetchRSSLinks(rssListURL)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取RSS链接失败: %v", err))
		return
	}
	if len(rssLinks) == 0 {
		_ = appendLog(ctx, "[WARN] RSS列表为空, 无需抓取")
		return
	}

	// 并发抓取所有RSS，获取结果和问题统计
	results, problems := fetchAllFeeds(ctx, rssLinks, defaultAvatar)

	// 提取成功抓取的项，并做按发布时间的倒序排序
	var itemsWithTime []struct {
		article Article
		t       time.Time
	}
	var successCount int
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		successCount++
		itemsWithTime = append(itemsWithTime, struct {
			article Article
			t       time.Time
		}{*r.Article, r.ParsedTime})
	}

	// 按发布时间倒序排序
	sort.Slice(itemsWithTime, func(i, j int) bool {
		return itemsWithTime[i].t.After(itemsWithTime[j].t)
	})

	// 整理所有文章到一个切片
	var allItems []Article
	for _, v := range itemsWithTime {
		allItems = append(allItems, v.article)
	}

	// 构造输出数据结构，并 JSON 序列化
	allData := AllData{
		Items:   allItems,
		Updated: time.Now().Format("2006年01月02日 15:04:05"),
	}
	jsonBytes, err := json.MarshalIndent(allData, "", "  ")
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON序列化失败: %v", err))
		return
	}

	// 上传到 COS
	if err := uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes); err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v", err))
		return
	}

	// 写执行日志
	logSummary := summarizeResults(successCount, len(rssLinks), problems)
	_ = appendLog(ctx, logSummary)
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
//   - string: 整理好的日志信息
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

	// 如果没有任何问题，则输出“无警告或错误”的提示
	if len(parseFails) == 0 && len(feedEmpties) == 0 && len(noAvatarList) == 0 && len(brokenAvatarList) == 0 {
		sb.WriteString("没有任何警告或错误, 一切正常\n")
	}
	return sb.String()
}
