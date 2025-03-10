// 作者: 游钓四方 <haibao1027@gmail.com>
// 文件: cos_upload.go
// 说明: 使用COS SDK将data.json文件上传到指定Bucket路径

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// uploadToCos 使用cos-go-sdk-v5将data.json覆盖上传到指定Bucket
// 【游钓四方 <haibao1027@gmail.com>】
// 参数:
//   - ctx       : 上下文
//   - secretID  : 腾讯云COS SecretID
//   - secretKey : 腾讯云COS SecretKey
//   - dataURL   : data.json在COS中的完整路径(包含https://...)
//   - data      : 要上传的json字节内容
//
// 返回:
//   - error: 如果上传出现错误, 返回错误; 否则nil
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
	u, err := url.Parse(dataURL)
	if err != nil {
		return wrapErrorf(err, "解析dataURL失败: %s", dataURL)
	}
	baseURL := &cos.BaseURL{
		BucketURL: &url.URL{
			Scheme: u.Scheme,
			Host:   u.Host,
		},
	}
	client := cos.NewClient(baseURL, &http.Client{
		Transport: &cos.AuthorizationTransport{
			SecretID:  secretID,
			SecretKey: secretKey,
		},
	})
	key := strings.TrimPrefix(u.Path, "/")

	_, err = client.Object.Put(ctx, key, strings.NewReader(string(data)), nil)
	if err != nil {
		return wrapErrorf(err, "上传至COS失败")
	}
	return nil
}
