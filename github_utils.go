// Author: 游钓四方 <haibao1027@gmail.com>
// File: github_utils.go
// Description: 主要是与GitHub进行文件操作的工具函数 (获取SHA、更新文件、删除文件等)
// Technical documentation:
// GitHub REST API: https://docs.github.com/zh/rest?apiVersion=2022-11-28

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

// getGitHubFileSHA 获取指定仓库内某个路径文件的SHA,若文件不存在则返回空
//
// Description:
//
//	通过 GitHub API 获取指定仓库中文件的 sha 值，用于后续更新或删除操作
//	如果文件不存在，返回空字符串
//
// Parameters:
//   - ctx   : 上下文，用于控制取消或超时
//   - token : GitHub的访问令牌
//   - owner : 仓库所有者用户名
//   - repo  : 仓库名
//   - path  : 仓库内文件的相对路径
//
// Returns:
//   - string: 文件的SHA，如果文件不存在或出错可能为空
//   - error : 如果请求过程中出现错误，返回错误；否则返回 nil
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

	// 404 表示文件不存在，直接返回空SHA
	if resp.StatusCode == 404 {
		return "", nil
	}
	// 如果不是200，则认为获取失败
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
//
// Description:
//
//	该函数通过 GitHub API 调用来在指定仓库和分支里创建或更新文件
//	当 sha 不为空时会执行更新逻辑，sha 为空时会执行创建逻辑
//
// Parameters:
//   - ctx            : 上下文
//   - token          : GitHub Token
//   - owner, repo    : 仓库所有者 & 仓库名
//   - path, sha      : 要更新或创建的文件路径，以及文件的旧SHA（可为空）
//   - content        : 要写入的文件内容（原始文本，内部会进行Base64编码）
//   - commitMsg      : 提交信息
//   - committerName  : 提交者姓名
//   - committerEmail : 提交者邮箱
//
// Returns:
//   - error: 如果出现错误则返回，否则返回nil
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
	// 如果已有文件, 则必须包含旧的SHA
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

	// 正常时返回 200（更新）或 201（创建）
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to put file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// deleteGitHubFile 删除GitHub仓库内的文件
//
// Description:
//
//	调用 GitHub API 删除指定的文件，需要提供文件SHA
//	该操作会在 main 分支上进行提交（删除操作算一次提交）
//
// Parameters:
//   - ctx            : 上下文
//   - token          : GitHub Token
//   - owner, repo    : 仓库所有者 & 仓库名
//   - path, sha      : 要删除的文件路径，以及文件的SHA
//   - committerName  : 提交者姓名
//   - committerEmail : 提交者邮箱
//
// Returns:
//   - error: 若出现错误则返回；无错误返回nil
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

	// 删除成功返回200
	if resp.StatusCode != 200 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete file %s, status: %d, body: %s",
			path, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

// listGitHubDir 列出GitHub仓库某目录下的文件与信息
//
// Description:
//
//	调用 GitHub API 获取指定目录下的所有文件/子目录，返回它们的名字、SHA、类型等信息
//	如果该目录不存在或为空，返回nil
//
// Parameters:
//   - ctx   : 上下文
//   - token : GitHub Token
//   - owner : 仓库所有者
//   - repo  : 仓库名
//   - dir   : 目标目录路径
//
// Returns:
//   - []struct{Name, SHA, Type}: 文件/目录信息列表
//   - error                    : 请求或解析错误
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

	// 404 表示目录不存在
	if resp.StatusCode == 404 {
		return nil, nil
	}
	// 200 正常，否则视为失败
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

// decodeBase64 对Base64字符串进行解码,并返回解码后的文本
//
// Description:
//
//	该函数简单地对传入的 Base64 字符串进行解码，并返回解码后的字符串
//
// Parameters:
//   - b64str: Base64 编码的字符串
//
// Returns:
//   - string: 解码后的普通字符串
//   - error : 如果解码失败则返回错误
func decodeBase64(b64str string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64str)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// uploadToGitHub 使用GitHub API 将 data.json 覆盖上传到指定仓库路径
//
// Parameters:
//   - ctx      : 上下文，用于在需要时取消操作
//   - token    : GitHub的访问令牌
//   - owner    : 仓库所有者用户名
//   - repo     : 仓库名
//   - filePath : data.json 在 GitHub 仓库中的完整路径(例如 "data/data.json")
//   - data     : 要上传的 JSON 字节内容
//
// Returns:
//   - error: 如果上传出现错误, 返回错误; 否则nil
func uploadToGitHub(ctx context.Context, token, owner, repo, filePath string, data []byte) error {
	// 获取提交者信息（可根据实际需求定制，也可以当作额外形参传进来）
	committerName := owner
	committerEmail := owner + "@users.noreply.github.com"

	// 先查文件是否存在，获取其SHA
	sha, err := getGitHubFileSHA(ctx, token, owner, repo, filePath)
	if err != nil {
		return wrapErrorf(err, "获取 %s 文件SHA失败", filePath)
	}

	// 通过 putGitHubFile 创建或更新文件
	err = putGitHubFile(
		ctx,
		token,
		owner,
		repo,
		filePath,
		sha,                // 如果文件原先不存在则 sha==""，putGitHubFile 会自动创建
		string(data),       // 这里需要传 string
		"Update data.json", // 提交信息
		committerName,
		committerEmail,
	)
	if err != nil {
		return wrapErrorf(err, "上传 data.json 失败")
	}
	return nil
}
