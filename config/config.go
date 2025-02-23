package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"
)

/*
@author: 游钓四方
@contact:  haibiao1027@gmail.com
@function: Config 用于存放项目所有的可配置信息，通过环境变量注入。
*/
type Config struct {
	SecretID         string
	SecretKey        string
	GithubToken      string
	GithubName       string
	GithubRepository string
	COSRSS           string
	MaxRetries       int
	RetryInterval    time.Duration
	MaxConcurrency   int
	HTTPTimeout      time.Duration
	DefaultAvatarURL string
	COSAvatar        string
	COSFavoriteRSS   string
	COSForeverBlog   string
	COSNameMapping   string
}

// AppConfig 全局配置实例
var AppConfig *Config

/*
@author: 游钓四方
@contact:  haibiao1027@gmail.com
@function: LoadConfig 使用 viper 从环境变量加载所有配置信息
@params:   无
@return:   error 如果必填变量缺失则返回错误，否则nil
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
		"NAME":                     cfg.GithubName,
		"REPOSITORY":               cfg.GithubRepository,
		"TOKEN":                    cfg.GithubToken,
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

// getEnvInt 从 viper 读取 int
func getEnvInt(key string, defaultValue int) int {
	valStr := viper.GetString(key)
	if valStr == "" {
		return defaultValue
	}
	if i, err := strconv.Atoi(valStr); err == nil {
		return i
	}
	return defaultValue
}

// getEnvDuration 从 viper 读取 time.Duration
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	valStr := viper.GetString(key)
	if valStr == "" {
		return defaultValue
	}
	if d, err := time.ParseDuration(valStr); err == nil {
		return d
	}
	return defaultValue
}
