// Author: 游钓四方 <haibao1027@gmail.com>
// File: github_utils.go
// Description: 主要是与GitHub进行文件操作的工具函数 (获取SHA、更新文件、删除文件等)

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// getGitHubFileSHA 获取指定仓库内某个路径文件的SHA; 若文件不存在则返回空
func getGitHubFileSHA(ctx context.Context, token, owner, repo, path string) (string, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}

	var response struct {
		SHA string `json:"sha"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	return response.SHA, nil
}

// putGitHubFile 创建或更新GitHub仓库内文件
// Parameters:
//   - ctx              : 上下文
//   - token            : GitHub Token
//   - owner, repo      : 仓库所有者 & 仓库名
//   - path, sha        : 要更新的文件路径, 以及旧文件的SHA(可为空)
//   - content          : 要写入的内容
//   - commitMsg        : 提交信息
//   - committerName    : 提交者姓名
//   - committerEmail   : 提交者邮箱
//
// Returns:
//   - error: 如果出现错误, 则返回相应的错误, 否则为nil
func putGitHubFile(ctx context.Context, token, owner, repo, path, sha, content, commitMsg, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	payload := map[string]interface{}{
		"message": commitMsg,
		"content": encoded,
		"branch":  "main",
		"committer": map[string]string{
			"name":  committerName,
			"email": committerEmail,
		},
	}
	if sha != "" {
		payload["sha"] = sha
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", apiURL, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to put file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// deleteGitHubFile 删除GitHub仓库内的文件
// Parameters:
//   - ctx              : 上下文
//   - token            : GitHub Token
//   - owner, repo      : 仓库所有者 & 仓库名
//   - path, sha        : 要删除的文件路径, 以及文件的SHA
//   - committerName    : 提交者姓名
//   - committerEmail   : 提交者邮箱
//
// Returns:
//   - error: 如果出现错误, 则返回错误; 否则为nil
func deleteGitHubFile(ctx context.Context, token, owner, repo, path, sha, committerName, committerEmail string) error {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)

	payload := map[string]interface{}{
		"message":   "Delete old log file",
		"sha":       sha,
		"branch":    "main",
		"committer": map[string]string{"name": committerName, "email": committerEmail},
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", apiURL, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// listGitHubDir 列出GitHub仓库某目录下的文件与信息
// Returns: 列表, 每个元素包含文件/目录名, SHA, 类型等; 若出错则error非nil
func listGitHubDir(ctx context.Context, token, owner, repo, dir string) ([]struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
	Type string `json:"type"`
}, error) {
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, dir)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list dir %s, status: %d, body: %s",
			dir, resp.StatusCode, string(bodyBytes))
	}

	var files []struct {
		Name string `json:"name"`
		SHA  string `json:"sha"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}

// decodeBase64 对Base64字符串进行解码, 并返回解码后的文本
func decodeBase64(b64str string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64str)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}
