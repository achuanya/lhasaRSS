package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"lhasaRSS/config"
	"lhasaRSS/logging"
	"lhasaRSS/pkg/rss"
)

func main() {
	// 加载配置
	if err := config.LoadConfig(); err != nil {
		logging.LogError(fmt.Errorf("配置加载失败: %w", err))
		os.Exit(1)
	}

	// 初始化 RSSProcessor
	processor := rss.NewRSSProcessor(config.AppConfig)
	defer processor.Close()

	// 记录开始时间，用于统计耗时
	start := time.Now()

	// 设置全局超时
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := processor.Run(ctx); err != nil {
		logging.LogError(fmt.Errorf("运行失败: %w", err))
	}

	// 输出本次运行的详细统计和性能数据，写入 summary 日志
	elapsed := time.Since(start)
	rss.PrintRunSummary(elapsed)

	// 关闭日志
	// logging.CloseLogger()
}
