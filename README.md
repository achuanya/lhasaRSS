# lhasaRSS

效果：[https://lhasa.icu/links.html](https://lhasa.icu/links.html)

本项目是一个RSS 抓取与聚合的工具，会从指定的RSS源列表中并发抓取最新文章，并将结果（包括博客名称、文章标题、发布时间、文章链接和头像）按照时间倒序输出到 data.json 文件，最后上传至腾讯云 COS 中指定的路径。同时还会将每次运行时的一些日志记录到 GitHub 仓库中（按日期分日志文件）

**主要功能**：

从 COS 上获取一个纯文本的 RSS 列表文件（每行一个RSS链接）
并发抓取每个 RSS 源，解析出最新的文章及相关信息
支持对解析失败、空Feed、头像缺失等情况进行统计并记录
将抓取结果保存成 data.json，再上传至腾讯云 COS
同步将执行日志写入到 GitHub 仓库中，方便查看历史记录
使用指数退避算法（exponential backoff）来重试解析失败的 RSS，减少因网络波动或 SSL 问题导致的抓取中断

1. **目录结构**

```txt
lhasaRSS
├── logs/            # 日志目录
├── data/
│ ├── data.json      # 程序抓取后生成并上传的 JSON 文件 (可选地存放在 GitHub 或 COS)
│ └── rss.txt        # 订阅源，(可选地存放在 GitHub 或 COS)
├── config.go        # 统一管理和校验环境变量
├── cos_upload.go    # 使用腾讯云 COS SDK 上传 data.json
├── feed_fetcher.go  # 并发抓取 RSS 核心逻辑，含指数退避重试等
├── feed_parser.go   # RSS 时间解析、头像解析等辅助函数
├── github_utils.go  # 与 GitHub 文件操作相关的工具函数 (创建、更新、删除等)
├── logger.go        # 将日志写入 GitHub 的 logs/ 目录；清理旧日志
├── main.go          # 程序入口，业务主流程调度
├── model.go         # 数据结构定义 (Article, AllData, feedResult)
├── wrap_error.go    # 包装错误信息时附带文件名和行号
└── go.mod           # Go Modules 依赖管理
``

2. **环境变量**
lhasaRSS 主要通过以下环境变量来进行配置：

| 变量名称                     | 说明                                                                                                                | 必填条件                                                                                                          |
|------------------------------|-----------------------------------------------------------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------|
| **TENCENT_CLOUD_SECRET_ID**  | 腾讯云 COS SecretID                                                                                                  | 当 `RSS_SOURCE=COS` **或** `SAVE_TARGET=COS` 时必须设置                                                           |
| **TENCENT_CLOUD_SECRET_KEY** | 腾讯云 COS SecretKey                                                                                                 | 当 `RSS_SOURCE=COS` **或** `SAVE_TARGET=COS` 时必须设置                                                           |
| **RSS_SOURCE**              | RSS 列表来源，可选值: `COS` / `GITHUB`。默认为 `GITHUB`                                                               | 若选择 `COS`，需要额外提供 `RSS` 环境变量指向远程 TXT 文件地址                                                    |
| **RSS**                     | RSS 列表文件位置：<br/>- 如果 `RSS_SOURCE=GITHUB`，则为本地路径(如 `data/rss.txt`)<br/>- 如果 `RSS_SOURCE=COS`，则为 HTTP(S) 远程 TXT 文件地址 | 当 `RSS_SOURCE=COS` 时必填；若 `RSS_SOURCE=GITHUB` 未指定，则默认为 `data/rss.txt`                                |
| **SAVE_TARGET**             | data.json 的存储位置，可选值：`COS` / `GITHUB`。默认为 `GITHUB`                                                        | 当选择 `COS` 时需要提供 `DATA` 环境变量                                                                           |
| **DATA**                    | data.json 保存目标：<br/>- 若 `SAVE_TARGET=GITHUB`，则为 GitHub 文件路径(如 `data/data.json`)<br/>- 若 `SAVE_TARGET=COS`，则为 HTTP(S) 上传路径(如 `https://<bucket>.cos.ap-<region>.myqcloud.com/folder/data.json`) | 当 `SAVE_TARGET=COS` 时必填；若 `SAVE_TARGET=GITHUB` 未指定，则默认为 `data/data.json`                            |
| **DEFAULT_AVATAR**          | 默认头像URL。若 RSS 无头像或头像URL失效，会回退到此地址                                                               | 可选                                                                                                              |
| **TOKEN**                   | GitHub Token                                                                                                          | 当 `SAVE_TARGET=GITHUB` 时必须设置                                                                                |
| **NAME**                    | GitHub 用户名                                                                                                          | 当 `SAVE_TARGET=GITHUB` 时必须设置                                                                                |
| **REPOSITORY**              | GitHub 仓库名（`owner/repo` 格式）                                                                                    | 当 `SAVE_TARGET=GITHUB` 时必须设置                                                                                |

> **Tips**: 当 `RSS_SOURCE` 和 `SAVE_TARGET` 均为 `GITHUB` 时，代表你只使用 GitHub 读写文件，那么所有腾讯云相关的环境变量都可以省略。

---

3. **部署与运行**

在 GitHub 上准备好一个空仓库并生成 Token（具有 repo 权限），用以写日志

在服务器或本地机配置上述环境变量（可在 CI/CD 平台上也可在本地 .env 文件里）

创建工作流文件示例：

在你的仓库中，点击 Actions > New workflow，新建一个 .yml 工作流文件，例如 .github/workflows/rss_update.yml

示例 Workflow（定时任务，每 1 小时跑一次）

```yml
name: lhasaRSS Update

on:
  schedule:
    - cron: '0 * * * *'    # 每1小时执行一次
  workflow_dispatch:       # 允许手动触发

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: write

    steps:
      - name: Check out repository code
        uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v3
        with:
          go-version: '1.24.0'

      - name: Build and Run
        env:
          TOKEN:                    ${{ secrets.TOKEN }}
          NAME:                     ${{ secrets.NAME }}
          REPOSITORY:               ${{ secrets.REPOSITORY }}
          # 以下两项仅在COS场景时才需要
          TENCENT_CLOUD_SECRET_ID:  ${{ secrets.TENCENT_CLOUD_SECRET_ID }}
          TENCENT_CLOUD_SECRET_KEY: ${{ secrets.TENCENT_CLOUD_SECRET_KEY }}
          DEFAULT_AVATAR:           ${{ secrets.DEFAULT_AVATAR }}
          RSS:                      ${{ secrets.RSS }}
          DATA:                     ${{ secrets.DATA }}
        # RSS_SOURCE:               ${{ secrets.RSS_SOURCE }}
        # SAVE_TARGET:              ${{ secrets.SAVE_TARGET }}
          RSS_SOURCE:               GITHUB
          # 如果 RSS_SOURCE=GITHUB，但需要指定具体路径:
          # RSS:                   data/rss.txt
          SAVE_TARGET:              GITHUB
          # 如果 SAVE_TARGET=GITHUB，但需要指定具体路径:
          # DATA:                  data/data.json
        run: |
          go mod tidy
          go build -o rssfetch .
          ./rssfetch
          echo "=== Done RSS Fetch ==="
```

1. 将所需的环境变量配置在仓库的 Settings > Secrets and variables > actions 中（以 secrets.TOKEN 等形式引用）

2. 如果你想把抓取后的 data.json 放在 COS 上，则把 SAVE_TARGET 改为 COS 并提供 DATA 等环境变量

提交后，GitHub Actions 会定时触发工作流，自动执行程序并上传RSS和日志，当然也可以手动调试

4. **日志查看**

抓取过程中，出现 解析失败、RSS为空、头像失效 等情况，会在 logs/2025-03-11.log (示例) 中追加记录

当天重复运行多次，会在同一个 .log 文件里不断追加新的时间戳行

程序会在每次运行后自动清理 7 天之前的 .log 文件，避免日志无限增多

5. **参考**
## 相关文档
* lhasaRSS:[https://github.com/achuanya/lhasaRSS][1]
* 腾讯 Go SDK 快速入门: [https://cloud.tencent.com/document/product/436/31215][2]
* XML Go SDK 源码: [https://github.com/tencentyun/cos-go-sdk-v5][3]
* GitHub REST API: [https://docs.github.com/zh/rest][4]
* 轻量级 RSS/Atom 解析库: [https://github.com/mmcdole/gofeed][5]

[1]:https://github.com/achuanya/lhasaRSS
[2]:https://cloud.tencent.com/document/product/436/31215
[3]:https://github.com/tencentyun/cos-go-sdk-v5
[4]:https://docs.github.com/zh/rest
[5]:https://github.com/mmcdole/gofeed