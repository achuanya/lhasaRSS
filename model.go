// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: model.go
// 说明: 定义数据结构、类型等

package main

import (
	"time"
)

// Article 结构体：只保留最关键的字段
// 游钓四方 <haibao1027@gmail.com>
//   - BlogName  : 博客名称
//   - Title     : 文章标题
//   - Published : 文章发布时间 (格式如 "09 Mar 2025")
//   - Link      : 文章链接
//   - Avatar    : 博客头像
type Article struct {
	BlogName  string `json:"blog_name"` // 博客名称
	Title     string `json:"title"`     // 文章标题
	Published string `json:"published"` // 文章发布时间 (已格式化为 "09 Mar 2025")
	Link      string `json:"link"`      // 文章链接
	Avatar    string `json:"avatar"`    // 博客头像
}

// AllData 结构体：用于最终输出 JSON
// 游钓四方 <haibao1027@gmail.com>
//   - Items   : 所有文章
//   - Updated : 数据更新时间（用中文格式字符串）
type AllData struct {
	Items   []Article `json:"items"`   // 所有文章
	Updated string    `json:"updated"` // 数据更新时间（用中文格式字符串）
}

// feedResult 用于并发抓取时，保存单个 RSS feed 的抓取结果（或错误信息）
// 游钓四方 <haibao1027@gmail.com>
type feedResult struct {
	Article    *Article  // 抓到的最新一篇文章（可能为 nil）
	FeedLink   string    // RSS 地址
	Err        error     // 抓取过程中的错误
	ParsedTime time.Time // 正确解析到的发布时间，用于后续排序
}
