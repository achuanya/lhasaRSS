// Author: 游钓四方 <haibao1027@gmail.com>
// File: config.go
// Description: 统一加载本项目所需的环境变量，并提供对外访问；在此处集中校验

package main

import (
	"fmt"
	"os"
	"strings"
)

// Config 用于存放本项目需要的所有环境变量
type Config struct {
	// 腾讯云相关
	TencentSecretID  string // 腾讯云 COS SecretID
	TencentSecretKey string // 腾讯云 COS SecretKey

	// RSS来源配置：
	// 当 RSS_SOURCE = "COS" 时，RssListURL 应为远程txt文件的HTTP地址(如 COS地址)
	// 当 RSS_SOURCE = "GITHUB" 时，RssListURL 可为本地路径，例如 "data/rss.txt"
	RssSource  string // "COS" 或 "GITHUB"
	RssListURL string // RSS列表txt文件的地址(远程或本地)

	// data.json 的目标存储配置
	// 可选值: "GITHUB" 或 "COS"
	// 若未设置, 默认存至 "GITHUB"
	SaveTarget    string
	DataURL       string // data.json 在COS或GitHub的完整路径
	DefaultAvatar string // 默认头像URL

	// GitHub 相关
	GitHubToken string // GitHub Token
	GitHubName  string // GitHub 用户名
	GitHubRepo  string // GitHub 仓库名
}

// LoadConfig 从系统环境变量中加载配置
//
// Description:
//
//	该函数仅做字符串读取，不做任何校验，后续可调用 cfg.Validate() 做集中校验
//	新增环境变量 RSS_SOURCE 用于区分 RSS 列表使用 COS 还是本地文件
func LoadConfig() *Config {
	cfg := &Config{
		TencentSecretID:  os.Getenv("TENCENT_CLOUD_SECRET_ID"),
		TencentSecretKey: os.Getenv("TENCENT_CLOUD_SECRET_KEY"),
		RssSource:        strings.ToUpper(os.Getenv("RSS_SOURCE")),
		RssListURL:       os.Getenv("RSS"),

		SaveTarget:    strings.ToUpper(os.Getenv("SAVE_TARGET")),
		DataURL:       os.Getenv("DATA"),
		DefaultAvatar: os.Getenv("DEFAULT_AVATAR"),

		GitHubToken: os.Getenv("TOKEN"),
		GitHubName:  os.Getenv("NAME"),
		GitHubRepo:  os.Getenv("REPOSITORY"),
	}

	// 默认值处理
	if cfg.RssSource == "" {
		// 若未显式设置，则默认从 GITHUB 读取 RSS 列表
		cfg.RssSource = "GITHUB"
	}
	if cfg.SaveTarget == "" {
		// 若未显式设置，则默认保存到 GitHub
		cfg.SaveTarget = "GITHUB"
	}

	return cfg
}

// Validate 对当前配置进行合法性校验，若必填字段缺失则返回错误
//
// Description:
//
//	本方法可在 main() 中调用，一次性校验所有必需环境变量，
//	避免在其他地方重复判断
func (cfg *Config) Validate() error {
	var missing []string

	if cfg.TencentSecretID == "" {
		missing = append(missing, "TENCENT_CLOUD_SECRET_ID")
	}
	if cfg.TencentSecretKey == "" {
		missing = append(missing, "TENCENT_CLOUD_SECRET_KEY")
	}

	// RSS_SOURCE与RSS
	if cfg.RssListURL == "" {
		missing = append(missing, "RSS")
	}

	if cfg.DataURL == "" {
		missing = append(missing, "DATA")
	}

	// 如果保存是 GITHUB, 则要求 GitHubToken, GitHubName, GitHubRepo 均不可为空
	if cfg.SaveTarget == "GITHUB" {
		if cfg.GitHubToken == "" {
			missing = append(missing, "TOKEN")
		}
		if cfg.GitHubName == "" {
			missing = append(missing, "NAME")
		}
		if cfg.GitHubRepo == "" {
			missing = append(missing, "REPOSITORY")
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("环境变量缺失: %v", missing)
	}

	return nil
}
