package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func main() {
	// 加载配置
	if err := LoadConfig(); err != nil {
		LogError(fmt.Errorf("配置加载失败: %w", err))
		os.Exit(1)
	}

	// 初始化
	processor := NewRSSProcessor(AppConfig)
	defer processor.Close()

	// 全局超时，3 分钟
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	if err := processor.Run(ctx); err != nil {
		LogError(fmt.Errorf("运行失败: %w", err))
	}

	// 优雅地关闭日志文件
	// CloseLogger()
}
