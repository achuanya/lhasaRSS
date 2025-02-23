package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// 全局通道
var (
	errorChan   = make(chan string, 1000)
	summaryChan = make(chan string, 100)
)

// 当前打开的日志文件
var (
	logMu       sync.Mutex
	errLogFile  *os.File
	sumLogFile  *os.File
	currentDate string
)

// init 初始化日志协程
func init() {
	// 保证 logs 文件夹存在
	_ = os.MkdirAll("logs", 0755)

	go errorLogWorker()
	go summaryLogWorker()

	// 每天 0 点创建新日志文件，并删除 7 天前的旧日志
	go rotateLogsDaily()
}

// LogError 记录错误日志（带文件与行号）
func LogError(err error) {
	if err == nil {
		return
	}
	_, file, line, _ := runtime.Caller(1)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	// 只取文件名(去掉路径)
	shortFile := file
	if idx := strings.LastIndex(file, "/"); idx != -1 {
		shortFile = file[idx+1:]
	}
	msg := fmt.Sprintf("[%s][错误] (%s:%d) %s", timestamp, shortFile, line, err.Error())
	errorChan <- msg
}

// LogSummary 记录汇总/统计信息
func LogSummary(msg string) {
	summaryChan <- msg
}

// errorLogWorker 单独写错误日志
func errorLogWorker() {
	for msg := range errorChan {
		logMu.Lock()
		ensureLogFile() // 确保日志文件已打开
		if errLogFile != nil {
			_, _ = errLogFile.WriteString(msg + "\n")
		}
		logMu.Unlock()
	}
}

// summaryLogWorker 单独写汇总日志
func summaryLogWorker() {
	for msg := range summaryChan {
		logMu.Lock()
		ensureLogFile()
		if sumLogFile != nil {
			_, _ = sumLogFile.WriteString(msg + "\n")
		}
		logMu.Unlock()
	}
}

// ensureLogFile 判断今天的日期是否变了，如果变了则重新打开日志文件
func ensureLogFile() {
	today := time.Now().Format("2006-01-02")
	if today == currentDate && errLogFile != nil && sumLogFile != nil {
		return
	}
	currentDate = today

	// 先关闭之前的文件
	if errLogFile != nil {
		_ = errLogFile.Close()
	}
	if sumLogFile != nil {
		_ = sumLogFile.Close()
	}

	// 打开新的文件
	errLogName := filepath.Join("logs", fmt.Sprintf("error-%s.log", today))
	sumLogName := filepath.Join("logs", fmt.Sprintf("summary-%s.log", today))

	var err error
	errLogFile, err = os.OpenFile(errLogName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("无法打开错误日志文件 %s: %v\n", errLogName, err)
		errLogFile = nil
	}
	sumLogFile, err = os.OpenFile(sumLogName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("无法打开汇总日志文件 %s: %v\n", sumLogName, err)
		sumLogFile = nil
	}
}

// rotateLogsDaily 每天 0 点执行一次，创建新的日志文件，并清理 7 天前的文件
func rotateLogsDaily() {
	for {
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		time.Sleep(nextMidnight.Sub(now))

		logMu.Lock()
		// 切换到新日志文件
		ensureLogFile()
		logMu.Unlock()

		// 清理 7 天前的文件
		cleanupOldLogs()
	}
}

// cleanupOldLogs 删除 logs/ 文件夹下 7 天前的日志文件
func cleanupOldLogs() {
	files, err := os.ReadDir("logs")
	if err != nil {
		return
	}
	deadline := time.Now().AddDate(0, 0, -7) // 7天前
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		// 期待文件名类似 error-2025-03-01.log 或 summary-2025-03-01.log
		parts := strings.Split(name, "-")
		if len(parts) < 2 {
			continue
		}
		dateStr := parts[len(parts)-1]                // 取最后一部分  2025-03-01.log
		dateStr = strings.TrimSuffix(dateStr, ".log") // 2025-03-01
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if t.Before(deadline) {
			_ = os.Remove(filepath.Join("logs", name))
		}
	}
}

// CloseLogger 可在程序结束时调用
func CloseLogger() {
	close(errorChan)
	close(summaryChan)
	logMu.Lock()
	defer logMu.Unlock()

	if errLogFile != nil {
		_ = errLogFile.Close()
	}
	if sumLogFile != nil {
		_ = sumLogFile.Close()
	}
}
