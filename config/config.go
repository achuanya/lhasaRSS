package config

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/viper"
)

// Config 配置
type Config struct {
	SecretID         string
	SecretKey        string
	GithubToken      string
	GithubName       string
	GithubRepository string
	COSURL           string
	MaxRetries       int
	RetryInterval    time.Duration
	MaxConcurrency   int
	HTTPTimeout      time.Duration
	DefaultAvatarURL string
}

// 全局配置实例
var AppConfig *Config

// LoadConfig 使用 viper 从环境变量加载配置
func LoadConfig() error {
	viper.AutomaticEnv()

	viper.SetDefault("MAX_RETRIES", 3)
	viper.SetDefault("RETRY_INTERVAL", 10*time.Second)
	viper.SetDefault("MAX_CONCURRENCY", 10)
	viper.SetDefault("HTTP_TIMEOUT", 15*time.Second)

	// 将 Github Actions 环境变量读到本地
	cfg := &Config{
		SecretID:         viper.GetString("TENCENT_CLOUD_SECRET_ID"),
		SecretKey:        viper.GetString("TENCENT_CLOUD_SECRET_KEY"),
		COSURL:           viper.GetString("COSURL"),
		DefaultAvatarURL: viper.GetString("DEFAULT_AVATAR_URL"),
		GithubToken:      viper.GetString("TOKEN"),
		GithubName:       viper.GetString("NAME"),
		GithubRepository: viper.GetString("REPOSITORY"),
	}

	// 处理 int / duration 类型
	cfg.MaxRetries = getEnvInt("MAX_RETRIES", 3)
	cfg.RetryInterval = getEnvDuration("RETRY_INTERVAL", 10*time.Second)
	cfg.MaxConcurrency = getEnvInt("MAX_CONCURRENCY", 10)
	cfg.HTTPTimeout = getEnvDuration("HTTP_TIMEOUT", 15*time.Second)

	// 数据验证
	// 如果用于 COS 或 GitHubToken 可以忽略
	required := map[string]string{
		"TENCENT_CLOUD_SECRET_ID":  cfg.SecretID,
		"TENCENT_CLOUD_SECRET_KEY": cfg.SecretKey,
		"COSURL":                   cfg.COSURL,
		"DEFAULT_AVATAR_URL":       cfg.DefaultAvatarURL,
		"TOKEN":                    cfg.GithubToken,
		"NAME":                     cfg.GithubName,
		"REPOSITORY":               cfg.GithubRepository,
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
