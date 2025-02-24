package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"
)

/*
@author: 游钓四方 <haibao1027@gmail.com>
@function: Config
@description: 用于存放项目所有的可配置信息，通过环境变量注入
*/
type Config struct {
	SecretID         string        // 腾讯云秘钥ID
	SecretKey        string        // 腾讯云秘钥Key
	GithubToken      string        // GitHub Token
	GithubName       string        // GitHub用户名
	GithubRepository string        // GitHub仓库名
	COSRSS           string        // 用于上传RSS
	MaxRetries       int           // 重试次数
	RetryInterval    time.Duration // 初始重试间隔
	MaxConcurrency   int           // 并发量
	HTTPTimeout      time.Duration // http 超时
	DefaultAvatarURL string        // 默认头像URL
	COSAvatar        string
	COSFavoriteRSS   string
	COSForeverBlog   string
	COSNameMapping   string
}

// AppConfig 全局配置实例
var AppConfig *Config

/*
@function: LoadConfig
@description: 使用viper从环境变量中加载配置，并做必要的检查
@return: error 如果必填项缺失则返回错误
*/
func LoadConfig() error {
	viper.AutomaticEnv()

	viper.SetDefault("MAX_RETRIES", 3)
	viper.SetDefault("RETRY_INTERVAL", 10*time.Second)
	viper.SetDefault("MAX_CONCURRENCY", 10)
	viper.SetDefault("HTTP_TIMEOUT", 15*time.Second)

	cfg := &Config{
		SecretID:         viper.GetString("TENCENT_CLOUD_SECRET_ID"),
		SecretKey:        viper.GetString("TENCENT_CLOUD_SECRET_KEY"),
		GithubToken:      viper.GetString("TOKEN"),
		GithubName:       viper.GetString("NAME"),
		GithubRepository: viper.GetString("REPOSITORY"),
		COSRSS:           viper.GetString("COS_RSS"),
		DefaultAvatarURL: viper.GetString("DEFAULT_AVATAR_URL"),
		COSAvatar:        viper.GetString("COS_AVATAR"),
		COSFavoriteRSS:   viper.GetString("COS_MY_FAVORITE_RSS"),
		COSForeverBlog:   viper.GetString("COS_FOREVER_BLOG"),
		COSNameMapping:   viper.GetString("COS_NAME_MAPPING"),
	}

	// getEnvInt 从 viper 读取 int
	cfg.MaxRetries = getEnvInt("MAX_RETRIES", 3)
	cfg.RetryInterval = getEnvDuration("RETRY_INTERVAL", 10*time.Second)
	cfg.MaxConcurrency = getEnvInt("MAX_CONCURRENCY", 10)
	cfg.HTTPTimeout = getEnvDuration("HTTP_TIMEOUT", 15*time.Second)

	// 数据验证
	// 如果用于 COS 或 GitHubToken 可以忽略
	required := map[string]string{
		"TENCENT_CLOUD_SECRET_ID":  cfg.SecretID,
		"TENCENT_CLOUD_SECRET_KEY": cfg.SecretKey,
		"TOKEN":                    cfg.GithubToken,
		"NAME":                     cfg.GithubName,
		"REPOSITORY":               cfg.GithubRepository,
		"COS_RSS":                  cfg.COSRSS,
		"DEFAULT_AVATAR_URL":       cfg.DefaultAvatarURL,
		"COS_AVATAR":               cfg.COSAvatar,
		"COS_MY_FAVORITE_RSS":      cfg.COSFavoriteRSS,
		"COS_FOREVER_BLOG":         cfg.COSForeverBlog,
		"COS_NAME_MAPPING":         cfg.COSNameMapping,
	}
	for k, v := range required {
		if v == "" {
			return fmt.Errorf("环境变量 %s 必须设置", k)
		}
	}

	AppConfig = cfg
	return nil
}

// getEnvInt  / getEnvDuration 用于辅助解析数值和时间
func getEnvInt(key string, defaultValue int) int {
	val := viper.GetString(key)
	if val == "" {
		return defaultValue
	}
	if i, err := strconv.Atoi(val); err == nil {
		return i
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	val := viper.GetString(key)
	if val == "" {
		return defaultValue
	}
	if d, err := time.ParseDuration(val); err == nil {
		return d
	}
	return defaultValue
}
