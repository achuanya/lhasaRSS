package utils

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"lhasaRSS/logging"
)

/*
@author: 游钓四方 <haibao1027@gmail.com>
@description:   工具包：
  - CleanXMLContent：去除无效字符
  - ParseTime：解析多种日期格式
  - FormatTime：格式化时间
  - WithRetry：指数退避算法
*/

var timeFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	time.RFC1123Z,
	time.RFC1123,
}

/*
@author: 游钓四方 <haibao1027@gmail.com>
@function: CleanXMLContent 清理字符串中的不可见字符
@params:   content string 原始RSS内容
@return:   string  清理后的内容
*/
func CleanXMLContent(content string) string {
	re := regexp.MustCompile(`[\x00-\x1F\x7F-\x9F]`)
	return re.ReplaceAllString(content, "")
}

/*
@function: ParseTime
@description: 依次按常见格式解析时间字符串,解析成功则返回
@params:   timeStr string
@return:   time.Time, error
*/
func ParseTime(timeStr string) (time.Time, error) {
	for _, fmtStr := range timeFormats {
		if t, err := time.Parse(fmtStr, timeStr); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

/*
@function: FormatTime
@description: 将time.Time格式化为 "January 2, 2006"
*/
func FormatTime(t time.Time) string {
	return t.Format("January 2, 2006")
}

/*
@function: WithRetry 使用指数退避算法对 fn 进行重试
@params:
  - ctx: 上下文
  - maxRetries: 最大重试次数
  - baseInterval: 初始等待间隔
  - fn: 需要执行的函数

@return:
  - T: fn 的返回结果
  - error: 最终失败错误

@explanation:

	第 i 次重试失败后，等待 baseInterval * 2^(i-1) 的时长，再继续尝试。
*/
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
