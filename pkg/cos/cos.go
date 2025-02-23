package cos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"lhasaRSS/config"
	"lhasaRSS/pkg/utils"

	gocos "github.com/tencentyun/cos-go-sdk-v5"
)

/*
@function: Article / AvatarData / ForeverData 是与 COS 交互时的JSON数据结构
*/
type Article struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Link       string `json:"link"`
	Date       string `json:"date"`
	Avatar     string `json:"avatar"`
}

type AvatarData struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar"`
}

type ForeverData struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Link       string `json:"link"`
	Date       string `json:"date"`
	Avatar     string `json:"avatar"`
}

/*
@author:

	游钓四方 <haibiao1027@gmail.com>

@function: InitCOSClient 根据 cfg.COSRSS 里的 BucketURL 创建一个腾讯云 COS 客户端，用于上传 rss_data.json。
@params:
  - cfg *config.Config

@return:
  - *gocos.Client  成功初始化后的 COS 客户端
*/
func InitCOSClient(cfg *config.Config) *gocos.Client {
	u, _ := url.Parse(cfg.COSRSS) // COS_RSS
	baseURL := &gocos.BaseURL{BucketURL: u}

	// 自定义 Transport
	customTransport := &http.Transport{
		MaxIdleConns:        cfg.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     cfg.MaxConcurrency,
		MaxIdleConnsPerHost: cfg.MaxConcurrency,
	}

	authTransport := &gocos.AuthorizationTransport{
		SecretID:  cfg.SecretID,
		SecretKey: cfg.SecretKey,
		Transport: customTransport,
	}

	httpClient := &http.Client{
		Transport: authTransport,
		Timeout:   cfg.HTTPTimeout,
	}

	return gocos.NewClient(baseURL, httpClient)
}

/*
@function: SaveArticlesToCOS 将最终的 articles 列表序列化成 JSON，上传到 COS (data/rss_data.json)
@params:
  - ctx context.Context
  - cfg *config.Config
  - client *gocos.Client
  - articles []Article

@return:
  - error

@description:

	调用 withRetry 对上传进行重试
*/
func SaveArticlesToCOS(
	ctx context.Context,
	cfg *config.Config,
	client *gocos.Client,
	articles []Article,
) error {
	// 这里不做排序，排序留给调用者决定也可。你若要按时间逆序，可自行 sort。
	jsonData, err := json.Marshal(articles)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	// data/rss_data.json 这个路径是示例，可根据需要修改
	_, err = utils.WithRetry(ctx, cfg.MaxRetries, cfg.RetryInterval, func() (interface{}, error) {
		resp, putErr := client.Object.Put(ctx, "data/rss_data.json", bytes.NewReader(jsonData), nil)
		if putErr != nil {
			return nil, fmt.Errorf("COS 上传失败: %w", putErr)
		}
		_ = resp.Body.Close()
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("最终上传失败: %w", err)
	}
	return nil
}
