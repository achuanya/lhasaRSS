package cos

import (
	"net/http"
	"net/url"
	"time"

	"lhasaRSS/config"

	gocos "github.com/tencentyun/cos-go-sdk-v5"
)

/*
  cos.go 仅放置 COS 相关的基础封装（如 InitCOSClient）和一些数据结构
*/

/*
Article, AvatarData, etc. 用于RSS解析或COS上传时的数据结构
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

/*
InitCOSClient: 根据 cfg.COSRSS 创建一个 cos.Client，用于后续上传文件
*/
func InitCOSClient(cfg *config.Config) *gocos.Client {
	u, _ := url.Parse(cfg.COSRSS)
	baseURL := &gocos.BaseURL{BucketURL: u}

	transport := &http.Transport{
		MaxIdleConns:        cfg.MaxConcurrency * 2,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		MaxConnsPerHost:     cfg.MaxConcurrency,
		MaxIdleConnsPerHost: cfg.MaxConcurrency,
	}

	authTransport := &gocos.AuthorizationTransport{
		SecretID:  cfg.SecretID,
		SecretKey: cfg.SecretKey,
		Transport: transport,
	}

	client := &http.Client{
		Transport: authTransport,
		Timeout:   cfg.HTTPTimeout,
	}
	return gocos.NewClient(baseURL, client)
}
