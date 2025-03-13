// Author: 游钓四方 <haibao1027@gmail.com>
// File: main.go
// Description: 程序入口文件, 读取环境变量, 并进行业务逻辑调度

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// main 程序入口
//
// Description:
//  1. 加载并校验环境变量(SecretID, SecretKey, RSS, DATA, RSS_SOURCE等)
//  2. 拉取RSS列表并并发抓取
//  3. 将结果整合为 data.json 并根据 SAVE_TARGET 上传到GitHub或COS
//  4. 写执行日志到GitHub
func main() {
	ctx := context.Background()

	// 加载配置
	cfg := LoadConfig()
	// 校验配置（只需在此处集中校验一次）
	if err := cfg.Validate(); err != nil {
		// 这里可以将错误写入日志再退出
		_ = appendLog(ctx, "[ERROR] "+err.Error())
		return
	}

	// 拉取RSS列表
	rssLinks, err := fetchRSSLinks(cfg)
	if err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取RSS链接失败: %v", err))
		return
	}
	if len(rssLinks) == 0 {
		_ = appendLog(ctx, "[WARN] RSS列表为空, 无需抓取")
		return
	}

	// 并发抓取所有RSS，获取结果和问题统计
	results, problems := fetchAllFeeds(ctx, rssLinks, cfg.DefaultAvatar)

	// 提取成功抓取的项，并做按发布时间的倒序排序
	var itemsWithTime []struct {
		article Article
		t       time.Time
	}
	var successCount int
	for _, r := range results {
		if r.Err == nil {
			successCount++
			itemsWithTime = append(itemsWithTime, struct {
				article Article
				t       time.Time
			}{*r.Article, r.ParsedTime})
		}
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

	// 根据 SAVE_TARGET 判断保存路径
	switch cfg.SaveTarget {
	case "GITHUB":
		if err := uploadToGitHub(
			ctx,
			cfg.GitHubToken,
			cfg.GitHubName,
			cfg.GitHubRepo,
			cfg.DataURL,
			jsonBytes,
		); err != nil {
			_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 GitHub 失败: %v", err))
			return
		}

	case "COS":
		if err := uploadToCos(ctx, cfg.TencentSecretID, cfg.TencentSecretKey, cfg.DataURL, jsonBytes); err != nil {
			_ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v", err))
			return
		}

	default:
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] SAVE_TARGET 值无效: %s (只能是 'GITHUB' 或 'COS')", cfg.SaveTarget))
		return
	}

	// 写执行日志
	logSummary := summarizeResults(successCount, len(rssLinks), problems)
	_ = appendLog(ctx, logSummary)
}
