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

// articleToKey generates a unique, comparable string key for an Article.
// This key includes BlogName, Title, and Link. Published time is excluded as per requirements.
func articleToKey(a Article) string {
	return fmt.Sprintf("Blog:%s|Title:%s|Link:%s", a.BlogName, a.Title, a.Link)
}

// areArticlesIdentical checks if two slices of Article contain the same articles,
// regardless of their order.
func areArticlesIdentical(articles1, articles2 []Article) bool {
	if len(articles1) != len(articles2) {
		return false
	}

	map1 := make(map[string]int)
	for _, article := range articles1 {
		map1[articleToKey(article)]++
	}

	map2 := make(map[string]int)
	for _, article := range articles2 {
		map2[articleToKey(article)]++
	}

	if len(map1) != len(map2) { // Different number of unique articles
		return false
	}

	for key, count1 := range map1 {
		if count2, ok := map2[key]; !ok || count1 != count2 {
			return false
		}
	}
	return true
}

// getExistingData fetches and parses the existing data.json from GitHub or COS.
// Returns an empty slice if the file doesn't exist or cannot be parsed.
func getExistingData(ctx context.Context, cfg *Config) ([]Article, error) {
	var rawData []byte
	var err error

	switch cfg.SaveTarget {
	case "GITHUB":
		content, _, getErr := getGitHubFileContent(ctx, cfg.GitHubToken, cfg.GitHubName, cfg.GitHubRepo, cfg.DataURL)
		if getErr != nil {
			return nil, wrapErrorf(getErr, "从 GitHub 获取旧 data.json 失败")
		}
		if content == "" { // File doesn't exist or is empty
			return []Article{}, nil
		}
		rawData = []byte(content)
	case "COS":
		cosData, getErr := getCosFileContent(ctx, cfg.DataURL)
		if getErr != nil {
			return nil, wrapErrorf(getErr, "从 COS 获取旧 data.json 失败")
		}
		if cosData == nil { // File doesn't exist or is empty
			return []Article{}, nil
		}
		rawData = cosData
	default:
		return nil, fmt.Errorf("SAVE_TARGET 值无效: %s (只能是 'GITHUB' 或 'COS')", cfg.SaveTarget)
	}

	var existingAllData AllData
	if err = json.Unmarshal(rawData, &existingAllData); err != nil {
		// If unmarshalling fails, it might be an old format or corrupted file.
		// Treat as no existing valid data.
		fmt.Printf("[WARN] 解析旧 data.json 失败: %v. 将视作无有效旧数据.\n", err)
		return []Article{}, nil
	}
	return existingAllData.Items, nil
}

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

	// 创建并加载头像映射器
	avatarMapper := NewAvatarMapper(cfg)
	if err := avatarMapper.LoadAvatarMap(); err != nil {
		_ = appendLog(ctx, fmt.Sprintf("[WARN] 加载头像映射失败: %v", err))
		// 继续执行，不阻止程序运行
	}

	// 并发抓取所有RSS，获取结果和问题统计
	results, problems := fetchAllFeeds(ctx, rssLinks, cfg.DefaultAvatar, avatarMapper)

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
	var newArticles []Article
	for _, v := range itemsWithTime {
		newArticles = append(newArticles, v.article)
	}

	// 获取现有的数据进行比较
	existingArticles, err := getExistingData(ctx, cfg)
	if err != nil {
		// 记录错误，但仍尝试继续，因为获取旧数据失败不应阻止新数据的保存
		_ = appendLog(ctx, fmt.Sprintf("[ERROR] 获取旧数据用于比较时失败: %v", err))
	}

	if err == nil && areArticlesIdentical(newArticles, existingArticles) {
		fmt.Println("抓取到的文章与现有数据相同，无需更新。")
		_ = appendLog(ctx, "抓取到的文章与现有数据相同，无需更新。")
		return // 停止执行
	}

	// 构造输出数据结构，并 JSON 序列化
	allData := AllData{
		Items:   newArticles, // 使用 newArticles
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
