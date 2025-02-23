package cos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"lhasaRSS/config"
	"lhasaRSS/pkg/utils"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// Article RSS 抓取到的文章
type Article struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Link       string `json:"link"`
	Date       string `json:"date"`
	Avatar     string `json:"avatar"`
}

// AvatarData 存储在 COS 上的头像信息
type AvatarData struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar"`
}

// ForeverData “十年之约”额外数据结构（可扩展）
type ForeverData struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Link       string `json:"link"`
	Date       string `json:"date"`
	Avatar     string `json:"avatar"`
}

// InitCOSClient 根据配置创建一个 *cos.Client
func InitCOSClient(cfg *config.Config) *cos.Client {
	u, _ := url.Parse(cfg.COSURL)
	baseURL := &cos.BaseURL{BucketURL: u}

	// 自定义 Transport
	customTransport := &http.Transport{
		MaxIdleConns:        cfg.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     cfg.MaxConcurrency,
		MaxIdleConnsPerHost: cfg.MaxConcurrency,
	}

	authTransport := &cos.AuthorizationTransport{
		SecretID:  cfg.SecretID,
		SecretKey: cfg.SecretKey,
		Transport: customTransport,
	}

	httpClient := &http.Client{
		Transport: authTransport,
		Timeout:   cfg.HTTPTimeout,
	}

	return cos.NewClient(baseURL, httpClient)
}

// FetchCOSFile 从 COS 读取文本文件并返回字符串
func FetchCOSFile(ctx context.Context, client *cos.Client, path string) (string, error) {
	resp, err := client.Object.Get(ctx, path, nil)
	if err != nil {
		return "", fmt.Errorf("获取 COS 文件失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取 COS 内容失败: %w", err)
	}
	return string(data), nil
}

// SaveArticlesToCOS 将文章列表保存到 COS 的 data/rss_data.json
// 同时可添加其他逻辑（比如插入特殊数据）
func SaveArticlesToCOS(ctx context.Context, cfg *config.Config, client *cos.Client, articles []Article) error {
	// 对文章进行排序
	sort.Slice(articles, func(i, j int) bool {
		t1, err1 := time.Parse("January 2, 2006", articles[i].Date)
		t2, err2 := time.Parse("January 2, 2006", articles[j].Date)
		if err1 != nil {
			t1 = time.Time{}
		}
		if err2 != nil {
			t2 = time.Time{}
		}
		return t1.After(t2)
	})

	// JSON 序列化
	jsonData, err := json.Marshal(articles)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	// 带重试上传
	_, err = utils.WithRetry(ctx, cfg.MaxRetries, cfg.RetryInterval, func() (interface{}, error) {
		resp, err := client.Object.Put(ctx, "data/rss_data.json", bytes.NewReader(jsonData), nil)
		if err != nil {
			return nil, fmt.Errorf("COS 上传失败: %w", err)
		}
		_ = resp.Body.Close()
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("最终上传失败: %w", err)
	}
	return nil
}

// LoadForeverBlogData 加载固定数据
func LoadForeverBlogData(ctx context.Context, cfg *config.Config, client *cos.Client) (*ForeverData, error) {
	// 这里假设放在 COS 的 data/foreverblog.json
	content, err := FetchCOSFile(ctx, client, "data/foreverblog.json")
	if err != nil {
		return nil, err
	}
	var data ForeverData
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return nil, fmt.Errorf("解析 foreverblog.json 失败: %w", err)
	}
	return &data, nil
}
