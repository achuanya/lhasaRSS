// Author: 游钓四方 <haibao1027@gmail.com>
// File: config.go
// Description: 统一加载本项目所需的环境变量，并提供对外访问

package main

import (
	"os"
	"strings"
)

// Config 用于存放本项目需要的所有环境变量
type Config struct {
	TencentSecretID  string // 腾讯云 COS SecretID
	TencentSecretKey string // 腾讯云 COS SecretKey

	RssListURL    string // RSS 列表 TXT 在 COS 中的 URL
	DataURL       string // data.json 在 COS 中的 URL
	DefaultAvatar string // 默认头像 URL

	// 保存目标: "GITHUB" 或 "COS"
	// 若不设置或留空则默认为 "COS"
	SaveTarget string

	// GitHub 相关
	GitHubToken string // GitHub Token
	GitHubName  string // GitHub 用户名
	GitHubRepo  string // GitHub 仓库名
}

// LoadConfig 从系统环境变量中加载配置
func LoadConfig() *Config {
	cfg := &Config{
		TencentSecretID:  os.Getenv("TENCENT_CLOUD_SECRET_ID"),
		TencentSecretKey: os.Getenv("TENCENT_CLOUD_SECRET_KEY"),
		RssListURL:       "data/rss.txt",
		DataURL:          os.Getenv("DATA"),
		DefaultAvatar:    os.Getenv("DEFAULT_AVATAR"),
		SaveTarget:       os.Getenv("SAVE_TARGET"),

		GitHubToken: os.Getenv("TOKEN"),
		GitHubName:  os.Getenv("NAME"),
		GitHubRepo:  os.Getenv("REPOSITORY"),
	}

	if cfg.SaveTarget == "" {
		cfg.SaveTarget = "GITHUB" // 没有显式设置就默认 GITHUB
	} else {
		cfg.SaveTarget = strings.ToUpper(cfg.SaveTarget)
	}
	return cfg
}
