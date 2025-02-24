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

/*
@author:   游钓四方 <haibiao1027@gmail.com>
@function: main
@description: 加载配置->初始化->运行->输出结果->关闭日志
@params:   无
@return:   无(遇到严重错误时会 os.Exit(1))
*/
func main() {
	// 加载配置（通过 viper 环境变量读取）
	if err := config.LoadConfig(); err != nil {
		logging.LogError(fmt.Errorf("配置加载失败: %w", err))
		logging.CloseLogger()
		os.Exit(1)
	}

	// 初始化 RSSProcessor 后续并发抓取与处理
	processor := rss.NewRSSProcessor(config.AppConfig)
	defer processor.Close() // 退出时释放资源

	// 记录启动时间，用于统计耗时
	start := time.Now()

	// 设置全局超时上下文（3分钟）
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := processor.Run(ctx); err != nil {
		// 记录错误(写error.log)
		logging.LogError(fmt.Errorf("执行失败: %w", err))
		// 如果想让Actions显示失败,再exit(1)
		logging.CloseLogger()
		// os.Exit(1)
	}

	// 输出统计(写summary.log)
	elapsed := time.Since(start)
	rss.PrintRunSummary(elapsed)

	// 正常结束,关闭日志
	logging.CloseLogger()
}
