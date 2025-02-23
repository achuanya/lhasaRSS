package utils

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"lhasaRSS/logging"
)

var timeFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
}

// Clean XML
func CleanXMLContent(content string) string {
	re := regexp.MustCompile(`[\x00-\x1F\x7F-\x9F]`)
	return re.ReplaceAllString(content, "")
}

func ParseTime(timeStr string) (time.Time, error) {
	for _, format := range timeFormats {
		if t, err := time.Parse(format, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

func FormatTime(t time.Time) string {
	return t.Format("January 2, 2006")
}

// withRetry 使用 指数退避算法 进行重试
// maxRetries: 最大重试次数
// baseInterval: 初始等待间隔
// fn: 需要执行的函数
func WithRetry[T any](ctx context.Context, maxRetries int, baseInterval time.Duration, fn func() (T, error)) (T, error) {
	var result T
	var lastErr error
	delay := baseInterval

	for i := 1; i <= maxRetries; i++ {
		result, lastErr = fn()
		if lastErr == nil {
			return result, nil
		}
		logging.LogError(fmt.Errorf("第 %d/%d 次重试失败: %v", i, maxRetries, lastErr))

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return result, errors.New("操作被取消或超时")
		}
		delay *= 2
	}
	return result, fmt.Errorf("超过最大重试次数(%d): %w", maxRetries, lastErr)
}
