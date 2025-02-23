package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

var (
	// 记录错误信息
	errorChan = make(chan string, 1000)

	// 记录统计/汇总信息
	summaryChan = make(chan string, 100)

	logMu      sync.Mutex
	logFileErr *os.File
	logFileSum *os.File
)

// 启动日志协程
func init() {
	// 分别启动两个 Worker：一个写 error.log，一个写 summary.log
	go errorLogWorker()
	go summaryLogWorker()

	// 再启动一个定时协程，每天 0 点清空 error.log
	go rotateErrorLogDaily()
}

// LogError 记录错误信息到 error.log，并带上文件名和行号
func LogError(err error) {
	if err == nil {
		return
	}
	// 获取调用方的文件和行号（runtime.Caller(1) 表示追溯上一层调用栈）
	_, file, line, _ := runtime.Caller(1)
	timestamp := getBeijingTime().Format("2006-01-02 15:04:05")
	msg := fmt.Sprintf("[%s][错误] (位置：%s:%d) %s", timestamp, file, line, err.Error())
	errorChan <- msg
}

// LogSummary 用于记录一些汇总信息、统计信息等（写到 summary.log）
func LogSummary(message string) {
	summaryChan <- message
}

// errorLogWorker 写入 error.log
func errorLogWorker() {
	var err error
	// 初次创建或追加
	logFileErr, err = os.OpenFile("error.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("无法打开或创建 error.log：", err)
		return
	}

	for msg := range errorChan {
		logMu.Lock()
		_, _ = logFileErr.WriteString(msg + "\n")
		logMu.Unlock()
	}
}

// summaryLogWorker 写入 summary.log
func summaryLogWorker() {
	var err error
	// 初次创建或追加
	logFileSum, err = os.OpenFile("summary.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("无法打开或创建 summary.log：", err)
		return
	}

	for msg := range summaryChan {
		logMu.Lock()
		_, _ = logFileSum.WriteString(msg + "\n")
		logMu.Unlock()
	}
}

// rotateErrorLogDaily 每天 0 点清空 error.log
func rotateErrorLogDaily() {
	for {
		now := time.Now()
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		time.Sleep(nextMidnight.Sub(now))

		logMu.Lock()
		if logFileErr != nil {
			_ = logFileErr.Close()
		}
		f, err := os.Create("error.log") // 覆盖写
		if err != nil {
			fmt.Println("无法清空 error.log：", err)
		} else {
			logFileErr = f
		}
		logMu.Unlock()
	}
}

// CloseLogger 在 main 退出前调用可关闭所有通道并关闭文件
func CloseLogger() {
	close(errorChan)
	close(summaryChan)
	logMu.Lock()
	defer logMu.Unlock()

	if logFileErr != nil {
		_ = logFileErr.Close()
	}
	if logFileSum != nil {
		_ = logFileSum.Close()
	}
}

// getBeijingTime 返回当前北京时间
func getBeijingTime() time.Time {
	return time.Now().In(time.FixedZone("CST", 8*3600))
}
