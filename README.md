# lhasaRSS —— 高效稳定的RSS聚合解决方案

<div align="center">
  <a href="https://996.icu" target="_blank">
      <img src="https://cos.lhasa.icu/svg/link-996.icu-red.svg" alt="996.icu" />
  </a>
  <img src="https://img.shields.io/github/actions/workflow/status/achuanya/lhasaRSS/rss_update.yml?branch=main" alt="GitHub Workflow Status" />
  <img src="https://img.shields.io/github/issues/achuanya/lhasaRSS" alt="GitHub issues" />
  <img src="https://img.shields.io/github/last-commit/achuanya/lhasaRSS" alt="GitHub last commit" />
  <img src="https://img.shields.io/github/stars/achuanya/lhasaRSS?style=social" alt="GitHub stars" />
  <img src="https://img.shields.io/github/license/achuanya/lhasaRSS" alt="GitHub license" />
  <img src="https://img.shields.io/github/go-mod/go-version/achuanya/lhasaRSS" alt="Go version" />
  <img src="https://img.shields.io/github/languages/code-size/achuanya/lhasaRSS" alt="Code size" />
  <img src="https://img.shields.io/tokei/lines/github/achuanya/lhasaRSS" alt="Lines of code" />
</div>

**效果展示**：[https://lhasa.icu/links.html](https://lhasa.icu/links.html)

lhasaRSS 是一款专注于 RSS 抓取与聚合的实用工具。它可以从预先设定的 RSS 源列表中并发抓取最新文章，自动提取博客名称、文章标题、发布时间、文章链接及头像等信息，并将数据按时间倒序存储到 `data.json` 文件中

随后上传至 Github 仓库亦或是腾讯云 COS 指定路径。同时，每次运行过程中的日志信息都会记录到 GitHub 仓库中（按日期生成独立日志文件），方便您随时查看和追溯历史记录

---

## 主要功能

- **获取 RSS 列表**  
  从 Github 或 腾讯云 COS 获取纯文本格式的 RSS 列表文件（每行一个 RSS 链接）

- **并发抓取与解析**  
  同时抓取各个 RSS 源，实时解析最新文章及相关信息

- **异常情况记录**  
  对解析失败、空 Feed、头像缺失等异常情况进行统计并记录，确保数据质量

- **数据存储与上传**  
  将抓取结果保存为 `data.json`，并自动上传至腾讯云 COS 或 GitHub 指定位置

- **日志记录与管理**  
  每次运行均会生成日志文件，并同步写入 GitHub，支持按日期分文件记录，并自动清理7天前的旧日志

- **指数退避重试机制**  
  采用指数退避算法重试解析失败的 RSS，提升系统稳定性，降低因网络波动或 SSL 问题引起的抓取中断风险

---

## 目录结构


```txt
lhasaRSS
├── logs/            # 日志目录
├── data/
│   ├── data.json    # 抓取后生成并上传的 JSON 文件 (可存放在 GitHub 或 COS)
│   └── rss.txt      # RSS 订阅源文件 (可存放在 GitHub 或 COS)
├── config.go        # 环境变量的统一管理和校验
├── cos_upload.go    # 利用腾讯云 COS SDK 上传 data.json
├── feed_fetcher.go  # 核心抓取逻辑（支持并发、指数退避重试等）
├── feed_parser.go   # 辅助函数（RSS 时间解析、头像处理等）
├── github_utils.go  # GitHub 文件操作工具（创建、更新、删除等）
├── logger.go        # 日志写入 GitHub 的 logs/ 目录及旧日志清理
├── main.go          # 主入口，业务流程调度
├── model.go         # 数据结构定义（Article、AllData、feedResult）
├── wrap_error.go    # 错误信息包装（附带文件名和行号）
└── go.mod           # Go Modules 依赖管理
```

## 环境变量

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

## 部署与运行

1.准备 GitHub 仓库

  在 GitHub 上创建一个空仓库，并生成具有 repo 权限的 Token 以便写入日志

2.配置环境变量

  在服务器或本地机配置上述环境变量（可通过 CI/CD 平台或本地 .env 文件进行配置）

3.创建工作流文件
  
  在仓库中点击 Actions > New workflow，新建一个 .yml 工作流文件，如 .github/workflows/rss_update.yml

  示例 Workflow（定时任务，每 1 小时执行一次）：
  
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

## 日志查看

在抓取过程中，如遇到解析失败、RSS 为空、头像无效等情况，系统会在类似 logs/2025-03-11.log 的日志文件中记录详细信息

当天多次运行时，日志将持续追加于同一文件中，同时程序会自动清理 7 天前的日志文件，确保日志存储高效且不臃肿

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