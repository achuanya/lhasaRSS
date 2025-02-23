package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// timeFormats 用于解析时间的预设格式
var timeFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
}

// cleanXMLContent 清理无效字符
func cleanXMLContent(content string) string {
	re := regexp.MustCompile(`[\x00-\x1F\x7F-\x9F]`)
	return re.ReplaceAllString(content, "")
}

// parseTime 尝试按多种格式解析时间
func parseTime(timeStr string) (time.Time, error) {
	for _, format := range timeFormats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

// formatTime 用于最终格式化输出
func formatTime(t time.Time) string {
	// 例如：January 2, 2006
	return t.Format("January 2, 2006")
}

// withRetry 使用指数退避策略进行重试
// maxRetries: 最大重试次数
// baseInterval: 初始等待间隔
// fn: 需要执行的函数
func withRetry[T any](ctx context.Context, maxRetries int, baseInterval time.Duration, fn func() (T, error)) (T, error) {
	var result T
	var lastErr error

	delay := baseInterval
	for i := 1; i <= maxRetries; i++ {
		result, lastErr = fn()
		if lastErr == nil {
			return result, nil
		}

		// 记录重试日志 error.log
		LogError(fmt.Errorf("第 %d/%d 次重试失败：%v", i, maxRetries, lastErr))

		// 指数退避
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return result, errors.New("操作被取消或超时")
		}
		delay = delay * 2 // 每次翻倍
	}

	return result, fmt.Errorf("超过最大重试次数(%d): %w", maxRetries, lastErr)
}
