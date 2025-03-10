package main

import (
    // -------------------- 引入所需包 --------------------
    "context"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "sync"
    "time"

    "github.com/mmcdole/gofeed"
    "github.com/tencentyun/cos-go-sdk-v5"
    "golang.org/x/net/html"
)

/* ==================== 数据结构定义 ==================== */

// Article 结构体：只保留最关键的字段
type Article struct {
    BlogName  string `json:"blog_name"` // 博客名称
    Title     string `json:"title"`     // 文章标题
    Published string `json:"published"` // 文章发布时间 (已格式化为 "09 Mar 2025")
    Link      string `json:"link"`      // 文章链接
    Avatar    string `json:"avatar"`    // 博客头像
}

// AllData 结构体：用于最终输出 JSON
type AllData struct {
    Items   []Article `json:"items"`   // 所有文章
    Updated string    `json:"updated"` // 数据更新时间（用中文格式字符串）
}

// feedResult 用于并发抓取时，保存单个 RSS feed 的抓取结果（或错误信息）
type feedResult struct {
    Article    *Article  // 抓到的最新一篇文章（可能为 nil）
    FeedLink   string    // RSS 地址
    Err        error     // 抓取过程中的错误
    ParsedTime time.Time // 正确解析到的发布时间，用于后续排序
}

/* ==================== 时间解析相关函数 ==================== */

// parseTime 尝试用多种格式解析 RSS 中的时间字符串，若都失败则返回错误
func parseTime(timeStr string) (time.Time, error) {
    // 定义可能出现的多种时间格式
    formats := []string{
        time.RFC1123Z,                   // "Mon, 02 Jan 2006 15:04:05 -0700"
        time.RFC1123,                    // "Mon, 02 Jan 2006 15:04:05 MST"
        time.RFC3339,                    // "2006-01-02T15:04:05Z07:00"
        "2006-01-02T15:04:05.000Z07:00", // "2025-02-09T13:20:27.000Z"
        "Mon, 02 Jan 2006 15:04:05 +0000",
    }

    // 依次尝试解析
    for _, f := range formats {
        if t, err := time.Parse(f, timeStr); err == nil {
            return t, nil
        }
    }
    // 如果都失败，就返回错误
    return time.Time{}, fmt.Errorf("无法解析时间: %s", timeStr)
}

/* ==================== 头像处理相关函数 ==================== */

// getFeedAvatarURL 尝试从 feed.Image 或者博客主页获取头像地址
func getFeedAvatarURL(feed *gofeed.Feed) string {
    // 如果 RSS 中存在 <image> 标签且 URL 不为空，则优先使用
    if feed.Image != nil && feed.Image.URL != "" {
        return feed.Image.URL
    }
    // 否则，如果 feed.Link 不为空，就尝试访问该链接获取头像
    if feed.Link != "" {
        return fetchBlogLogo(feed.Link)
    }
    // 如果以上都不行，就返回空字符串，后续再做默认头像处理
    return ""
}

// fetchBlogLogo 尝试抓取博客主页的 HTML，并从 <head> 中获取最常见的 icon；若没有则 fallback 到 favicon.ico
func fetchBlogLogo(blogURL string) string {
    // 1. 请求博客主页
    resp, err := http.Get(blogURL)
    if err != nil {
        // 如果请求失败，直接退回到 fallbackFavicon
        return fallbackFavicon(blogURL)
    }
    defer resp.Body.Close()

    // 如果响应状态不是 200，则也使用 fallback
    if resp.StatusCode != 200 {
        return fallbackFavicon(blogURL)
    }

    // 2. 解析 HTML，寻找 <link rel="icon"> / <link rel="shortcut icon"> / <link rel="apple-touch-icon"> / <meta property="og:image">
    doc, err := html.Parse(resp.Body)
    if err != nil {
        return fallbackFavicon(blogURL)
    }

    // 用于存储解析到的 icon 和 og:image
    var iconHref string
   [thinking]

 var ogImage string

    // 递归函数，遍历整棵 DOM 树
    var f func(*html.Node)
    f = func(n *html.Node) {
        if n.Type == html.ElementNode {
            tagName := strings.ToLower(n.Data)
            // 处理 <link ...> 标签
            if tagName == "link" {
                var relVal, hrefVal string
                for _, attr := range n.Attr {
                    switch strings.ToLower(attr.Key) {
                    case "rel":
                        relVal = strings.ToLower(attr.Val)
                    case "href":
                        hrefVal = attr.Val
                    }
                }
                // 如果 rel 中包含 "icon" 就认为它是网站图标
                if strings.Contains(relVal, "icon") && hrefVal != "" {
                    if iconHref == "" {
                        iconHref = hrefVal
                    }
                }
            } else if tagName == "meta" {
                // 处理 <meta ...> 标签
                var propVal, contentVal string
                for _, attr := range n.Attr {
                    switch strings.ToLower(attr.Key) {
                    case "property":
                        propVal = strings.ToLower(attr.Val)
                    case "content":
                        contentVal = attr.Val
                    }
                }
                if propVal == "og:image" && contentVal != "" {
                    ogImage = contentVal
                }
            }
        }
        // 继续遍历子节点
        for c := n.FirstChild; c != nil; c = c.NextSibling {
            f(c)
        }
    }
    f(doc)

    if iconHref != "" {
        return makeAbsoluteURL(blogURL, iconHref)
    }
    if ogImage != "" {
        return makeAbsoluteURL(blogURL, ogImage)
    }
    // 如果都没有，就 fallback 到 /favicon.ico
    return fallbackFavicon(blogURL)
}

// fallbackFavicon 解析出域名，然后返回 "scheme://host/favicon.ico"
func fallbackFavicon(blogURL string) string {
    u, err := url.Parse(blogURL)
    if err != nil {
        return ""
    }
    if u.Scheme == "" || u.Host == "" {
        return ""
    }
    return fmt.Sprintf("%s://%s/favicon.ico", u.Scheme, u.Host)
}

// makeAbsoluteURL 将相对路径转换为绝对路径
func makeAbsoluteURL(baseStr, refStr string) string {
    baseURL, err := url.Parse(baseStr)
    if err != nil {
        return refStr
    }
    refURL, err := url.Parse(refStr)
    if err != nil {
        return refStr
    }
    return baseURL.ResolveReference(refURL).String()
}

// checkURLAvailable 通过 HEAD 请求检查某个 URL 是否可以正常访问(返回 200)
func checkURLAvailable(urlStr string) (bool, error) {
    client := &http.Client{
        Timeout: 5 * time.Second, // 设置超时时间防止阻塞
    }
    req, err := http.NewRequest("HEAD", urlStr, nil)
    if err != nil {
        return false, err
    }
    resp, err := client.Do(req)
    if err != nil {
        return false, err
    }
    defer resp.Body.Close()
    return (resp.StatusCode == http.StatusOK), nil
}

/* ==================== GitHub 日志写入相关函数 ==================== */

// getGitHubFileSHA 获取指定仓库内某个路径文件的 SHA；若文件不存在则返回空
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
        return "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
    }

    var response struct {
        SHA string `json:"sha"`
    }
    if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
        return "", err
    }
    return response.SHA, nil
}

// getGitHubFileContent 获取指定文件的完整内容和 SHA
func getGitHubFileContent(ctx context.Context, token, owner, repo, path string) (string, string, error) {
    apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", owner, repo, path)
    req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
    if err != nil {
        return "", "", err
    }
    req.Header.Set("Authorization", "Bearer "+token)

    client := &http.Client{}
    resp, err := client.Do(req)
    if err != nil {
        return "", "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode == 404 {
        return "", "", nil
    }
    if resp.StatusCode != 200 {
        bodyBytes, _ := io.ReadAll(resp.Body)
        return "", "", fmt.Errorf("failed to get file %s, status: %d, body: %s", path, resp.StatusCode, string(bodyBytes))
    }

    var response struct {
        SHA     string `json:"sha"`
        Content string `json:"content"`
    }
    if err = json.NewDecoder(resp.Body).Decode(&response); err != nil {
        return "", "", err
    }

    decoded, err := base64.StdEncoding.DecodeString(response.Content)
    if err != nil {
        return "", "", err
    }
    return string(decoded), response.SHA, nil
}

// putGitHubFile 创建或更新 GitHub 仓库内的文件
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

// deleteGitHubFile 删除 GitHub 仓库内的文件
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

// listGitHubDir 列出 GitHub 仓库某目录下的文件与信息
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

// appendLog 函数：用于将日志内容写入 GitHub 仓库的 logs/YYYY-MM-DD.log 文件，并清理7天前的日志
func appendLog(ctx context.Context, rawLogContent string) error {
    token := os.Getenv("TOKEN")
    githubUser := os.Getenv("NAME")
    repoName := os.Getenv("REPOSITORY")
    owner := githubUser
    repo := repoName

    committerName := githubUser
    committerEmail := githubUser + "@users.noreply.github.com"

    // 日志文件名：logs/2025-03-10.log (示例)
    dateStr := time.Now().Format("2006-01-02")
    logPath := filepath.Join("logs", dateStr+".log")

    // 1. 获取旧内容（如果有）
    oldContent, oldSHA, err := getGitHubFileContent(ctx, token, owner, repo, logPath)
    if err != nil {
        return err
    }

    // 2. 在新内容每行前面加上时间戳（原代码亦是如此，这里保持）
    var sb strings.Builder
    timestamp := time.Now().Format("2006-01-02 15:04:05")
    lines := strings.Split(rawLogContent, "\n")
    for _, line := range lines {
        line = strings.TrimSpace(line)
        if line == "" {
            continue
        }
        sb.WriteString(fmt.Sprintf("[%s] %s\n", timestamp, line))
    }
    newLogSegment := sb.String()
    newContent := oldContent + newLogSegment

    // 3. 上传更新到 GitHub
    err = putGitHubFile(ctx, token, owner, repo, logPath, oldSHA, newContent,
        "Update log: "+dateStr, committerName, committerEmail)
    if err != nil {
        return err
    }

    // 4. 删除 7 天前的日志文件
    return cleanOldLogs(ctx, token, owner, repo, committerName, committerEmail)
}

// cleanOldLogs 删除 7 天前的日志文件
func cleanOldLogs(ctx context.Context, token, owner, repo, committerName, committerEmail string) error {
    files, err := listGitHubDir(ctx, token, owner, repo, "logs")
    if err != nil {
        return nil
    }
    sevenDaysAgo := time.Now().AddDate(0, 0, -7)

    for _, f := range files {
        if f.Type != "file" {
            continue
        }
        matched, _ := regexp.MatchString(`^\d{4}-\d{2}-\d{2}\.log$`, f.Name)
        if !matched {
            continue
        }
        dateStr := strings.TrimSuffix(f.Name, ".log")
        t, err := time.Parse("2006-01-02", dateStr)
        if err != nil {
            continue
        }
        if t.Before(sevenDaysAgo) {
            path := filepath.Join("logs", f.Name)
            delErr := deleteGitHubFile(ctx, token, owner, repo, path, f.SHA, committerName, committerEmail)
            if delErr != nil {
                fmt.Printf("删除旧日志 %s 失败: %v\n", f.Name, delErr)
            } else {
                fmt.Printf("已删除旧日志 %s\n", f.Name)
            }
        }
    }
    return nil
}

/* ==================== 工具函数：非法字符清洗 / RSS列表获取 / COS上传 ==================== */

// sanitizeXML 将字符串中的非法 XML 字符过滤掉（或替换为空字符串）
func sanitizeXML(input string) string {
    var sb strings.Builder
    for _, r := range input {
        // 过滤掉除 \t, \n, \r 以外的小于0x20的控制字符
        if (r == 0x9) || (r == 0xA) || (r == 0xD) || (r >= 0x20) {
            sb.WriteRune(r)
        } else {
            // 跳过无效控制字符
        }
    }
    return sb.String()
}

// fetchRSSLinks 从给定 URL 的文本文件逐行读取 RSS 链接
func fetchRSSLinks(rssListURL string) ([]string, error) {
    resp, err := http.Get(rssListURL)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("status code: %d", resp.StatusCode)
    }
    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }

    var links []string
    for _, line := range strings.Split(string(data), "\n") {
        line = strings.TrimSpace(line)
        if line != "" {
            links = append(links, line)
        }
    }
    return links, nil
}

// uploadToCos 使用 cos-go-sdk-v5，将 data.json 覆盖上传到对应 Bucket
func uploadToCos(ctx context.Context, secretID, secretKey, dataURL string, data []byte) error {
    u, err := url.Parse(dataURL)
    if err != nil {
        return err
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
    return err
}

/* ==================== 核心抓取逻辑 + 主函数 ==================== */

// fetchAndParseRSS 对单个 RSS 链接进行抓取，并用 sanitizeXML 过滤非法字符后再调用 gofeed 解析
func fetchAndParseRSS(rssLink string, fp *gofeed.Parser) (*gofeed.Feed, error) {
    // 1. 发送请求
    resp, err := http.Get(rssLink)
    if err != nil {
        return nil, fmt.Errorf("请求失败: %v", err)
    }
    defer resp.Body.Close()

    // 2. 读取 Body
    rawBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("读取失败: %v", err)
    }

    // 3. 清洗非法字符
    cleaned := sanitizeXML(string(rawBytes))

    // 4. 交给 gofeed 解析
    feed, err := fp.ParseString(cleaned)
    if err != nil {
        return nil, fmt.Errorf("解析 RSS 失败: %v", err)
    }
    return feed, nil
}

func main() {
    // 创建一个上下文，可在整个流程中使用
    ctx := context.Background()

    // 从环境变量读取所需配置
    secretID := os.Getenv("TENCENT_CLOUD_SECRET_ID")   // 腾讯云 COS SecretID
    secretKey := os.Getenv("TENCENT_CLOUD_SECRET_KEY") // 腾讯云 COS SecretKey
    rssListURL := os.Getenv("RSS")                     // 存放 RSS 链接的TXT文件地址
    dataURL := os.Getenv("DATA")                       // data.json 要上传到的 COS 路径
    defaultAvatar := os.Getenv("DEFAULT_AVATAR")       // 没有可用头像时的默认头像

    // 如果关键信息不全，则写日志并退出
    if secretID == "" || secretKey == "" || rssListURL == "" || dataURL == "" {
        _ = appendLog(ctx, "[ERROR] 环境变量缺失，请检查 TENCENT_CLOUD_SECRET_ID/TENCENT_CLOUD_SECRET_KEY/RSS/DATA 是否已配置。")
        return
    }
    if defaultAvatar == "" {
        _ = appendLog(ctx, "[WARN] 未设置 DEFAULT_AVATAR，将会导致无法访问头像时出现空字符串。")
    }

    // 1. 拉取 RSS 列表
    rssLinks, err := fetchRSSLinks(rssListURL)
    if err != nil {
        _ = appendLog(ctx, fmt.Sprintf("[ERROR] 拉取 RSS 链接失败: %v", err))
        return
    }
    if len(rssLinks) == 0 {
        _ = appendLog(ctx, "[WARN] RSS 列表为空，没有需要抓取的链接。")
        return
    }

    // 并发控制
    maxGoroutines := 10
    sem := make(chan struct{}, maxGoroutines)
    var wg sync.WaitGroup

    // 存放结果
    resultChan := make(chan feedResult, len(rssLinks))
    fp := gofeed.NewParser()

    // 2. 并发抓取
    for _, link := range rssLinks {
        link = strings.TrimSpace(link)
        if link == "" {
            continue
        }
        wg.Add(1)
        sem <- struct{}{} // 占用一个并发槽

        go func(rssLink string) {
            defer wg.Done()
            defer func() { <-sem }() // 释放并发槽

            var fr feedResult
            fr.FeedLink = rssLink

            feed, err := fetchAndParseRSS(rssLink, fp)
            if err != nil {
                fr.Err = err
                resultChan <- fr
                return
            }
            if feed == nil || len(feed.Items) == 0 {
                fr.Err = fmt.Errorf("该订阅没有内容")
                resultChan <- fr
                return
            }

[thinking]

            // 获取头像
            avatarURL := getFeedAvatarURL(feed)
            fr.Article = &Article{
                BlogName: feed.Title,
            }

            // 校验头像
            if avatarURL == "" {
                fr.Article.Avatar = ""
            } else {
                ok, _ := checkURLAvailable(avatarURL)
                if !ok {
                    fr.Article.Avatar = "BROKEN"
                } else {
                    fr.Article.Avatar = avatarURL
                }
            }

            // 只取最新的一篇
            latest := feed.Items[0]
            fr.Article.Title = latest.Title
            fr.Article.Link = latest.Link

            // 尝试解析发布时间
            pubTime := time.Now()
            if latest.PublishedParsed != nil {
                pubTime = *latest.PublishedParsed
            } else if latest.Published != "" {
                if t, e := parseTime(latest.Published); e == nil {
                    pubTime = t
                }
            }
            fr.ParsedTime = pubTime
            fr.Article.Published = pubTime.Format("02 Jan 2006") // 例如 "09 Mar 2025"

            resultChan <- fr
        }(link)
    }

    // 等待所有 goroutine 完成
    go func() {
        wg.Wait()
        close(resultChan)
    }()

    /* 统计信息相关的临时结构 */
    var itemsWithTime []struct {
        article Article
        t       time.Time
        link    string
    }

    // 记录各种可能的问题，用于最终写日志
    var parseFails []string       // RSS 解析失败 / 请求失败
    var feedEmpties []string      // 无内容
    var noAvatarList []string     // 头像字段为空
    var brokenAvatarList []string // 头像无法访问
    var successCount int          // 成功抓取计数

    // 3. 收集结果
    for r := range resultChan {
        if r.Err != nil {
            // 判断是解析失败还是 feed 没内容
            if strings.Contains(r.Err.Error(), "解析 RSS 失败") || strings.Contains(r.Err.Error(), "请求失败") {
                parseFails = append(parseFails, r.FeedLink)
            } else if strings.Contains(r.Err.Error(), "没有内容") {
                feedEmpties = append(feedEmpties, r.FeedLink)
            }
            continue
        }

        // 正常拿到Article
        successCount++

        // 检查头像
        if r.Article.Avatar == "" {
            noAvatarList = append(noAvatarList, r.FeedLink)
            r.Article.Avatar = defaultAvatar
        } else if r.Article.Avatar == "BROKEN" {
            brokenAvatarList = append(brokenAvatarList, r.FeedLink)
            r.Article.Avatar = defaultAvatar
        }

        // 收集到最终集合里
        itemsWithTime = append(itemsWithTime, struct {
            article Article
            t       time.Time
            link    string
        }{
            article: *r.Article,
            t:       r.ParsedTime,
            link:    r.FeedLink,
        })
    }

    // 4. 按发布时间“倒序”
    sort.Slice(itemsWithTime, func(i, j int) bool {
        return itemsWithTime[i].t.After(itemsWithTime[j].t)
    })

    // 组装到最终输出
    var allItems []Article
    for _, v := range itemsWithTime {
        allItems = append(allItems, v.article)
    }

    // 5. 组装 JSON
    allData := AllData{
        Items:   allItems,
        Updated: time.Now().Format("2006年01月02日 15:04:05"), // 中文格式时间
    }
    jsonBytes, err := json.MarshalIndent(allData, "", "  ")
    if err != nil {
        _ = appendLog(ctx, fmt.Sprintf("[ERROR] JSON 序列化失败: %v", err))
        return
    }

    // 6. 上传 data.json 到腾讯云 COS
    err = uploadToCos(ctx, secretID, secretKey, dataURL, jsonBytes)
    if err != nil {
        _ = appendLog(ctx, fmt.Sprintf("[ERROR] 上传 data.json 到 COS 失败: %v", err))
        return
    }

    // ====================== 还原“之前的日志输出格式”在此处 ======================
    var sb strings.Builder
    sb.WriteString("本次订阅抓取结果统计如下：\n")

    // 统计成功数
    sb.WriteString(fmt.Sprintf("✅ 成功抓取 %d 条订阅。\n", successCount))

    // 解析/请求失败统计
    if len(parseFails) > 0 {
        sb.WriteString(fmt.Sprintf("❌ 有 %d 条订阅解析失败或请求失败：\n", len(parseFails)))
        for _, l := range parseFails {
            sb.WriteString("  - " + l + "\n")
        }
    }

    // 无内容
    if len(feedEmpties) > 0 {
        sb.WriteString(fmt.Sprintf("⚠️ 有 %d 条订阅为空：\n", len(feedEmpties)))
        for _, l := range feedEmpties {
            sb.WriteString("  - " + l + "\n")
        }
    }

    // 头像字段为空
    if len(noAvatarList) > 0 {
        sb.WriteString(fmt.Sprintf("🖼️ 有 %d 条订阅头像字段为空，已使用默认头像：\n", len(noAvatarList)))
        for _, l := range noAvatarList {
            sb.WriteString("  - " + l + "\n")
        }
    }

    // 头像无法访问
    if len(brokenAvatarList) > 0 {
        sb.WriteString(fmt.Sprintf("🖼️ 有 %d 条订阅头像无法访问，已使用默认头像：\n", len(brokenAvatarList)))
        for _, l := range brokenAvatarList {
            sb.WriteString("  - " + l + "\n")
        }
    }

    // 若所有错误都没有
    if len(parseFails) == 0 && len(feedEmpties) == 0 && len(noAvatarList) == 0 && len(brokenAvatarList) == 0 {
        sb.WriteString("没有任何警告或错误，一切正常。\n")
    }

    // 写入日志
    _ = appendLog(ctx, sb.String())
}