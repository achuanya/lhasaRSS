package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	errorChan = make(chan string, 1000) // 专门用于错误日志的通道
	logMu     sync.Mutex
	logFile   *os.File // 用于写 error.log 的文件句柄
)

// init 中启动两个后台协程：一个负责写日志，一个负责每天 0 点清空日志
func init() {
	go errorLogWorker()
	go rotateLogDaily()
}

// LogError 将错误信息（或需要记录为错误的消息）发送到异步通道
// 统一在这里加上中文前缀、时间戳等
func LogError(err error) {
	if err == nil {
		return
	}
	message := fmt.Sprintf("[%s][错误] %s", getBeijingTime().Format("2006-01-02 15:04:05"), err.Error())
	errorChan <- message
}

// errorLogWorker 从通道中读取错误日志并写入到 error.log
func errorLogWorker() {
	var err error

	// 首次启动时，尝试以追加方式打开（若不存在则创建）
	logFile, err = os.OpenFile("error.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// 如果连日志文件都打开失败，就只能打印到控制台了
		fmt.Println("无法打开或创建 error.log 文件：", err)
		return
	}

	for msg := range errorChan {
		logMu.Lock()
		// 写入并换行
		_, _ = logFile.WriteString(msg + "\n")
		logMu.Unlock()
	}
}

// rotateLogDaily 每天 0 点清空 error.log （覆盖写）
func rotateLogDaily() {
	for {
		now := time.Now()
		// 计算下一个零点
		nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
		sleepDuration := nextMidnight.Sub(now)
		time.Sleep(sleepDuration)

		logMu.Lock()
		if logFile != nil {
			_ = logFile.Close()
		}
		// 使用 os.Create() 覆盖写，达到清空目的
		f, err := os.Create("error.log")
		if err != nil {
			fmt.Println("无法清空 error.log：", err)
		} else {
			logFile = f
		}
		logMu.Unlock()
	}
}

// getBeijingTime 返回当前北京时间
func getBeijingTime() time.Time {
	return time.Now().In(time.FixedZone("CST", 8*3600))
}

// CloseLogger 可以在 main 退出前调用，保证通道被关闭，worker 协程退出。
// 如果你希望优雅关闭日志，调用此方法即可（示例中可选）。
func CloseLogger() {
	close(errorChan)
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		_ = logFile.Close()
	}
}
