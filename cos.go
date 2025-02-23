package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"time"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// Article 代表抓取到的文章
type Article struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Link       string `json:"link"`
	Date       string `json:"date"`
	Avatar     string `json:"avatar"`
}

// AvatarData 代表存储在 COS 上的头像信息
type AvatarData struct {
	DomainName string `json:"domainName"`
	Name       string `json:"name"`
	Avatar     string `json:"avatar"`
}

// fetchCOSFile 从 COS 中获取文件内容
func (p *RSSProcessor) fetchCOSFile(ctx context.Context, path string) (string, error) {
	resp, err := p.cosClient.Object.Get(ctx, path, nil)
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

// saveToCOS 将文章数据保存到 COS
func (p *RSSProcessor) saveToCOS(ctx context.Context, articles []Article) error {
	// 十年之约，手动插入一条文章
	articles = append(articles, Article{
		DomainName: "https://foreverblog.cn",
		Name:       "十年之约",
		Title:      "穿梭虫洞-随机访问十年之约友链博客",
		Link:       "https://foreverblog.cn/go.html",
		Date:       "January 01, 2000",
		Avatar:     "https://cos.lhasa.icu/LinksAvatar/foreverblog.cn.png",
	})

	// 按日期倒序排序
	sort.Slice(articles, func(i, j int) bool {
		t1, err1 := time.Parse("January 2, 2006", articles[i].Date)
		t2, err2 := time.Parse("January 2, 2006", articles[j].Date)

		// 解析失败的视为最早
		if err1 != nil {
			t1 = time.Time{}
		}
		if err2 != nil {
			t2 = time.Time{}
		}
		return t1.After(t2)
	})

	jsonData, err := json.Marshal(articles)
	if err != nil {
		return fmt.Errorf("JSON 序列化失败: %w", err)
	}

	// 带重试的上传
	_, err = withRetry(ctx, p.config.MaxRetries, p.config.RetryInterval, func() (interface{}, error) {
		resp, err := p.cosClient.Object.Put(ctx, "data/rss_data.json", bytes.NewReader(jsonData), nil)
		if err != nil {
			return nil, fmt.Errorf("COS 上传失败: %w", err)
		}
		defer resp.Body.Close()
		return nil, nil
	})

	if err != nil {
		return fmt.Errorf("最终上传失败: %w", err)
	}

	// 不再记录成功日志
	return nil
}

// initCOSClient 根据配置初始化一个 COS 客户端
func initCOSClient(config *Config) *cos.Client {
	u, _ := url.Parse(config.COSURL)
	baseURL := &cos.BaseURL{BucketURL: u}

	cosClient := cos.NewClient(baseURL, nil)
	// 这里给 Transport 加上鉴权
	cosClient.Client.Transport = &cos.AuthorizationTransport{
		SecretID:  config.SecretID,
		SecretKey: config.SecretKey,
	}

	return cosClient
}
