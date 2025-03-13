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

// envWithDefault 用于获取系统环境变量，若不存在则返回默认值
func envWithDefault(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

// LoadConfig 从系统环境变量中加载配置
//
// Description:
//
//	该函数仅做字符串读取，不做任何校验，后续可调用 cfg.Validate() 做集中校验
//	新增环境变量 RSS_SOURCE 用于区分 RSS 列表使用 COS 还是本地文件
func LoadConfig() *Config {

	// 先将 RSS_SOURCE、SAVE_TARGET 统一转换为大写，方便后续判断
	rssSource := strings.ToUpper(envWithDefault("RSS_SOURCE", "GITHUB"))
	saveTarget := strings.ToUpper(envWithDefault("SAVE_TARGET", "GITHUB"))

	// 分别处理 RssListURL 和 DataURL 默认值：只有在对应模式下才赋默认值
	rssListURL := envWithDefault("RSS", "")
	if rssSource == "GITHUB" && rssListURL == "" {
		rssListURL = "data/rss.txt"
	}

	dataURL := envWithDefault("DATA", "")
	if saveTarget == "GITHUB" && dataURL == "" {
		dataURL = "data/data.json"
	}

	cfg := &Config{
		TencentSecretID:  os.Getenv("TENCENT_CLOUD_SECRET_ID"),
		TencentSecretKey: os.Getenv("TENCENT_CLOUD_SECRET_KEY"),

		RssSource:  rssSource,
		RssListURL: rssListURL,

		SaveTarget:    saveTarget,
		DataURL:       dataURL,
		DefaultAvatar: envWithDefault("DEFAULT_AVATAR", "https://cn.gravatar.com/avatar"),

		GitHubToken: os.Getenv("TOKEN"),
		GitHubName:  os.Getenv("NAME"),
		GitHubRepo:  os.Getenv("REPOSITORY"),
	}

	return cfg
}

// Validate 对当前配置进行合法性校验，若必填字段缺失则返回错误
//
// Description:
//
//	本方法可在 main() 中调用，一次性校验所有必需环境变量
func (cfg *Config) Validate() error {
	var missing []string

	// 当 RSS_SOURCE 或 SAVE_TARGET 需要使用 COS 时，需校验腾讯云配置
	if cfg.RssSource == "COS" || cfg.SaveTarget == "COS" {
		if cfg.TencentSecretID == "" {
			missing = append(missing, "TENCENT_CLOUD_SECRET_ID")
		}
		if cfg.TencentSecretKey == "" {
			missing = append(missing, "TENCENT_CLOUD_SECRET_KEY")
		}
	}

	// RSS_SOURCE = COS 时需提供 RSS (RssListURL)
	if cfg.RssSource == "COS" && cfg.RssListURL == "" {
		missing = append(missing, "RSS")
	}

	// SAVE_TARGET = COS 时需提供 DATA (DataURL)
	if cfg.SaveTarget == "COS" && cfg.DataURL == "" {
		missing = append(missing, "DATA")
	}

	// 如果保存到 GITHUB，必须提供 GitHub 相关配置
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
