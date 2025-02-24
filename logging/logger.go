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

/*
@author: 游钓四方 <haibao1027@gmail.com>
@description:
  - 日志系统:
  - 只记录错误到 error-YYYY-MM-DD.log
  - 记录汇总信息到 summary-YYYY-MM-DD.log
  - 每天 0 点时，自动切换新文件，并清理 7 天前的旧日志
*/

var (
	// 日志通道
	errorChan   = make(chan string, 1000)
	summaryChan = make(chan string, 100)

	// 文件句柄 & 同步锁
	logMu       sync.Mutex
	errLogFile  *os.File
	sumLogFile  *os.File
	currentDate string
)

// init：启动协程
func init() {
	_ = os.MkdirAll("logs", 0755)

	// 先手动锁定,再创建当日日志文,再解锁
	logMu.Lock()
	ensureLogFile()
	logMu.Unlock()

	// 启动两个后台 worker
	go errorLogWorker()
	go summaryLogWorker()

	// 启动每天0点切换日志,并清理7天前的旧文件
	go rotateLogsDaily()
}

/**
 * @function: LogError
 * @description: 记录错误信息到 error-YYYY-MM-DD.log 会带上调用方的文件名与行号
 */
func LogError(err error) {
	if err == nil {
		return
	}
	// 获取调用栈信息
	_, file, line, _ := runtime.Caller(1)
	shortFile := file
	if idx := strings.LastIndex(file, "/"); idx != -1 {
		shortFile = file[idx+1:]
	}
	// 合并日志内容
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("[%s][错误](%s:%d) %s", timestamp, shortFile, line, err.Error())
	errorChan <- msg
}

/*
@function: LogSummary 记录汇总/统计信息到 summary-YYYY-MM-DD.log
*/
func LogSummary(msg string) {
	summaryChan <- msg
}

// errorLogWorker & summaryLogWorker 分别从通道读日志并写入文件
func errorLogWorker() {
	for msg := range errorChan {
		logMu.Lock()
		ensureLogFile()
		if errLogFile != nil {
			_, _ = errLogFile.WriteString(msg + "\n")
		}
		logMu.Unlock()
	}
}

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

// ensureLogFile 如果日期变了就切换文件, 程序启动时也会手动调用一次
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

	// 创建/追加新的
	errFileName := filepath.Join("logs", fmt.Sprintf("error-%s.log", today))
	sumFileName := filepath.Join("logs", fmt.Sprintf("summary-%s.log", today))

	var err error
	errLogFile, err = os.OpenFile(errFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("无法打开错误日志文件 %s: %v\n", errFileName, err)
		errLogFile = nil
	}
	sumLogFile, err = os.OpenFile(sumFileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("无法打开汇总日志文件 %s: %v\n", sumFileName, err)
		sumLogFile = nil
	}
}

// rotateLogsDaily 每天0点切换新文件，并清理7天前的旧文件
func rotateLogsDaily() {
	for {
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		time.Sleep(nextMidnight.Sub(now))

		logMu.Lock()
		ensureLogFile() // 新一天,新文件
		logMu.Unlock()

		cleanupOldLogs() // 保留7天,删除更早的
	}
}

// cleanupOldLogs 删除7天前的日志文件
func cleanupOldLogs() {
	files, err := os.ReadDir("logs")
	if err != nil {
		return
	}
	deadline := time.Now().AddDate(0, 0, -7)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		// 解析日期, 例如:error-2025-03-01.log => dateStr=2025-03-01
		parts := strings.Split(name, "-")
		if len(parts) < 2 {
			continue
		}
		dateStr := strings.TrimSuffix(parts[len(parts)-1], ".log")
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if t.Before(deadline) {
			_ = os.Remove(filepath.Join("logs", name))
		}
	}
}

// CloseLogger 手动结束前,可调用以关闭通道并关闭文件
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
