// Author: 游钓四方 <haibao1027@gmail.com>
// File: model.go
// Description: 定义数据结构、类型等

package main

import (
	"time"
)

// Article 关键字段
//
// Description:
//
//	表示一篇文章及其所属博客的关键信息，比如博客名称、文章标题、发布时间、链接和头像URL
type Article struct {
	BlogName  string `json:"blog_name"` // 博客名称
	Title     string `json:"title"`     // 文章标题
	Published string `json:"published"` // 文章发布时间 (已格式化，如 "Mar 09, 2025")
	Link      string `json:"link"`      // 文章链接
	Avatar    string `json:"avatar"`    // 博客头像
}

// AllData 用于最终输出 JSON
//
// Description:
//
//	包含文章条目，及更新日期格式（中文格式的时间字符串）
type AllData struct {
	Items   []Article `json:"items"`   // 所有文章条目
	Updated string    `json:"updated"` // 数据更新时间（如 "2025年03月09日 15:04:05"）
}

// feedResult 用于并发抓取时，保存单个 RSS feed 的抓取结果（或错误信息）
//
// Description:
//
//	每抓取一个RSS源时产生一个 feedResult，记录成功时提取的文章信息，或记录失败错误
type feedResult struct {
	Article    *Article  // 抓取到的最新一篇文章（若失败则为 nil）
	FeedLink   string    // RSS 地址
	Err        error     // 抓取过程中的错误
	ParsedTime time.Time // 正确解析到的发布时间，用于后续对抓取结果排序
}
