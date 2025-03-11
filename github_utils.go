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

// getGitHubFileSHA 获取指定仓库内某个路径文件的SHA，若文件不存在则返回空
//
// Description:
//
//	通过 GitHub API 获取指定仓库中文件的 sha 值，用于后续更新或删除操作
//	如果文件不存在，返回空字符串
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
//
// Description:
//
//	调用 GitHub API 获取指定目录下的所有文件/子目录，返回它们的名字、SHA、类型等信息
//	如果该目录不存在或为空，返回nil
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
func decodeBase64(b64str string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(b64str)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// uploadToGitHub 使用 GitHub API 将 data.json 覆盖上传到指定仓库路径
func uploadToGitHub(
	ctx context.Context,
	token string,
	owner string,
	repo string,
	dataFilePath string,
	data []byte,
) error {

	committerName := owner
	committerEmail := owner + "@users.noreply.github.com"

	// 先查文件是否存在
	sha, err := getGitHubFileSHA(ctx, token, owner, repo, dataFilePath)
	if err != nil {
		return wrapErrorf(err, "获取 %s 文件SHA失败", dataFilePath)
	}

	// 通过 putGitHubFile 创建或更新
	err = putGitHubFile(
		ctx,
		token,
		owner,
		repo,
		dataFilePath,
		sha,
		string(data),
		"Update data.json",
		committerName,
		committerEmail,
	)
	if err != nil {
		return wrapErrorf(err, "上传 data.json 失败")
	}
	return nil
}
