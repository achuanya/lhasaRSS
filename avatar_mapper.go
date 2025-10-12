package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AvatarMapping 表示头像映射的数据结构
type AvatarMapping struct {
	Link   string `json:"link"`
	Avatar string `json:"avatar"`
}

// AvatarMapData 表示整个avatar.json文件的数据结构
type AvatarMapData struct {
	Data []AvatarMapping `json:"data"`
}

// AvatarMapper 头像映射器
type AvatarMapper struct {
	avatarMap map[string]string // 域名到头像URL的映射
	config    *Config
}

// NewAvatarMapper 创建新的头像映射器
func NewAvatarMapper(config *Config) *AvatarMapper {
	return &AvatarMapper{
		avatarMap: make(map[string]string),
		config:    config,
	}
}

// LoadAvatarMap 从远程URL加载头像映射数据
func (am *AvatarMapper) LoadAvatarMap() error {
	if am.config.AvatarMapURL == "" {
		return fmt.Errorf("avatar map URL not configured")
	}

	// 创建HTTP客户端，设置超时
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 发送GET请求
	resp, err := client.Get(am.config.AvatarMapURL)
	if err != nil {
		return fmt.Errorf("failed to fetch avatar map: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch avatar map, status code: %d", resp.StatusCode)
	}

	// 读取响应体
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read avatar map response: %w", err)
	}

	// 解析JSON数据
	var avatarData AvatarMapData
	if err := json.Unmarshal(body, &avatarData); err != nil {
		return fmt.Errorf("failed to parse avatar map JSON: %w", err)
	}

	// 构建域名到头像的映射
	am.avatarMap = make(map[string]string)
	for _, mapping := range avatarData.Data {
		domain := am.extractDomain(mapping.Link)
		if domain != "" {
			am.avatarMap[domain] = mapping.Avatar
		}
	}

	return nil
}

// extractDomain 从URL中提取域名
func (am *AvatarMapper) extractDomain(urlStr string) string {
	// 如果URL不包含协议，添加http://前缀
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "http://" + urlStr
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}

	// 返回主机名（域名）
	return strings.ToLower(parsedURL.Host)
}

// GetAvatarByDomain 根据域名获取对应的头像URL
func (am *AvatarMapper) GetAvatarByDomain(domain string) (string, bool) {
	domain = strings.ToLower(domain)
	avatar, exists := am.avatarMap[domain]
	return avatar, exists
}

// GetAvatarByURL 根据URL获取对应的头像URL
func (am *AvatarMapper) GetAvatarByURL(urlStr string) (string, bool) {
	domain := am.extractDomain(urlStr)
	if domain == "" {
		return "", false
	}
	return am.GetAvatarByDomain(domain)
}

// GetMappingCount 获取映射数量
func (am *AvatarMapper) GetMappingCount() int {
	return len(am.avatarMap)
}
