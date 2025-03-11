// Author: 游钓四方 <haibao1027@gmail.com>
// File: cos_upload.go
// Description: 使用COS SDK将data.json文件上传到指定Bucket路径
// Technical documentation:
// 腾讯 Go SDK 快速入门: https://cloud.tencent.com/document/product/436/31215
// XML Go SDK 源码: https://github.com/tencentyun/cos-go-sdk-v5

package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/tencentyun/cos-go-sdk-v5"
)

// uploadToCos 使用cos-go-sdk-v5将data.json覆盖上传到指定Bucket
//
// Parameters:
//   - ctx       : 上下文，用于在需要时取消操作
//   - secretID  : 腾讯云COS的 SecretID，用于身份认证
//   - secretKey : 腾讯云COS的 SecretKey，用于身份认证
//   - dataURL   : data.json 在COS中的完整路径(包含 https://...)
//   - data      : 要上传的 JSON 字节内容
//
// Returns:
//   - error: 如果上传出现错误, 返回错误; 否则nil
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
