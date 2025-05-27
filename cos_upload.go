// Author: 游钓四方 <haibao1027@gmail.com>
// File: cos_upload.go
// Description: 使用COS SDK将data.json文件上传到指定Bucket路径
// Technical documentation:
// 腾讯 Go SDK 快速入门: https://cloud.tencent.com/document/product/436/31215
// XML Go SDK 源码: https://github.com/tencentyun/cos-go-sdk-v5

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// uploadToCos 使用cos-go-sdk-v5将data.json覆盖上传到指定Bucket
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
	u, err := url.Parse(dataURL)
	if err != nil {
		// 如果 dataURL 无法被正常解析，这里会返回一个带有文件名和行号的包装错误
		return wrapErrorf(err, "解析dataURL失败: %s", dataURL)
	}
	// 创建COS的BaseURL，主要作用是设定BucketURL的Scheme与Host
	baseURL := &cos.BaseURL{
		BucketURL: &url.URL{
			Scheme: u.Scheme, // 协议，如 https
			Host:   u.Host,   // 主机名，如 xxx.cos.ap-xxxx.myqcloud.com
		},
	}
	// 使用授权信息（SecretID, SecretKey）创建COS客户端
	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})
	// 去掉路径开头的斜杠，得到对象名 key，例如 /folder/data.json => folder/data.json
	key := strings.TrimPrefix(u.Path, "/")

	// 调用 Put 接口将 data 的内容上传到 COS
	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	if err != nil {
		return wrapErrorf(err, "上传至COS失败")
	}
	return nil
}

// getCosFileContent fetches the content of a file from a given HTTP URL (typically a COS URL).
// Returns nil, nil if the file is not found (HTTP 404).
func getCosFileContent(ctx context.Context, dataURL string) ([]byte, error) {
	// Simpler version, matching fetchRSSLinksFromHTTP. Context is for consistency.
	resp, err := http.Get(dataURL)
	if err != nil {
		return nil, wrapErrorf(err, "无法获取COS文件: %s", dataURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // File not found, not an error for this function's purpose
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body) // Read body for more detailed error
		return nil, wrapErrorf(
			fmt.Errorf("HTTP状态码: %d %s, Body: %s", resp.StatusCode, http.StatusText(resp.StatusCode), string(bodyBytes)),
			"获取COS文件失败: %s", dataURL,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapErrorf(err, "读取COS文件body失败")
	}
	return data, nil
}
