# lhasaRSS

本项目是一个使用 Go 语言 编写的 RSS 抓取工具，可从指定的 COS（对象存储）上读取订阅列表（rss_feeds.txt）和头像数据（avatar_data.json），然后并发地请求每个 RSS 地址，解析最新文章信息，最终把结果（rss_data.json）上传回 COS。本项目包含以下主要功能：

并发爬取：支持配置最大并发数 MAX_CONCURRENCY，使用 Goroutine 和 Channel 工作池进行批量抓取，提高效率。
错误重试：使用自定义的 withRetry 进行指数退避，避免瞬时网络抖动导致的抓取失败。
头像映射：通过 COS 上的 avatar_data.json，实现为每个域名指定头像；如果找不到则使用默认头像。
日志系统：只记录错误日志到 error.log（带中文、文件行号），并在每天 0 点清空；同时在程序结束时把统计结果写入 summary.log。
配置管理：使用 viper 从环境变量中加载必要的配置（如 COS 的 SecretID、SecretKey 等），避免硬编码。
定制化统计：抓取成功数、失败数、解析失败数、找不到头像数、使用默认头像数，以及总耗时等。
主要技术/第三方库
Go 语言标准库：net/http（网络请求）、sync（并发互斥）、bufio/io（读写流）、time（时间处理）、runtime（获取文件和行号）等。
gofeed：解析 RSS/Atom 的第三方库，简化 RSS 格式的处理。
cos-go-sdk-v5：腾讯云 COS 官方 Go SDK，用于上传/下载对象到 COS。
viper：读取和管理配置项（环境变量），简化配置管理。


# flowchart LR

    A[启动程序<br>LoadConfig()] --> B[初始化RSSProcessor<br>包含httpClient和cosClient]
    B -->|创建上下文ctx| C[加载头像数据loadAvatars<br>从COS读取avatar_data.json]
    C --> D[加载订阅列表getFeeds<br>从COS读取rss_feeds.txt]
    D --> E[并发抓取fetchAllRSS]
    E -->|抓取与解析成功| F[得到Articles列表]
    E -->|出现错误| G[调用LogError<br>写入error.log]
    F --> H[上传结果到COS saveToCOS<br>rss_data.json]
    H --> I[统计结束<br>PrintRunSummary]
    I --> J[summary.log 记录汇总<br>程序结束]

LoadConfig：使用 viper 从环境变量读取配置，校验必填项。
NewRSSProcessor：创建一个包含自定义 http.Client 和 cos.Client 的处理器。
loadAvatars：从 COS 下载 avatar_data.json 并解析到内存中的 avatarMap。
getFeeds：从 COS 下载 rss_feeds.txt 并解析到 []string。
fetchAllRSS：并发地抓取所有 RSS（使用工作池 + withRetry 重试），过程中新建或更新统计信息；如遇错误，则调用 LogError 并加计数。
saveToCOS：将最终汇总的 []Article 排序后，序列化成 rss_data.json 上传到 COS。
PrintRunSummary：程序结束时，将统计结果（总数、成功数、失败数等）写到控制台并写进 summary.log。
error.log：在抓取/解析出现错误时同步写入。每天 0 点自动清空。

在没有任何错误的完美情况下，error.log 可能什么都不写；但 summary.log 会显示一份中文统计（成功数、耗时等）。
如果中途出现错误（比如某些 RSS 无法访问、解析失败、头像域名无效等），error.log 就会有清晰的文件、行号、中文提示，用于快速排查问题，同时 summary.log 也会显示失败数和错误详情统计。
如需在 GitHub Actions 的控制台上看到 error.log 或 summary.log 的内容，需要在你的工作流（YAML）中，添加类似 cat summary.log 或 cat error.log 的步骤，或者使用 actions/upload-artifact 把日志文件上传为构建产物。


本次运行完成！
总共需要处理的 RSS 数量：10
成功：8, 失败：2
时间/解析失败：1
找不到头像：1, 使用默认头像：1
总耗时：2.532117512s