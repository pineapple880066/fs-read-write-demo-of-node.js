# Agent Server Go 面试总结与题库

## 一、项目一句话介绍

这个项目是一个基于 Go 实现的 Agent 后端服务，核心目标不是单纯调用大模型接口，而是把“HTTP 接口层、JWT 鉴权、Redis 会话记忆、MySQL 持久化、RabbitMQ 异步任务、RAG 检索增强、模型网关、Prometheus 指标、OpenTelemetry 链路追踪、浏览器 UI”串成一个可运行、可降级、可扩展的智能问答服务。它既可以作为一个 SaaS 风格的多租户问答后端，也可以作为一个本地代码库问答服务。

如果面试官问“你这个项目解决了什么问题”，你可以直接回答：我做的是一个面向代码问答和知识导入场景的 Agent 服务端。它支持把文档或文件异步导入，切分成 chunks 存入 MySQL；查询时通过本地 Hybrid 检索或 TypeScript RAG 桥接召回上下文；对话时结合 Redis 里的短期记忆做上下文压缩，然后调用 OpenAI-compatible 模型接口完成回答；整个系统还加了 JWT、限流、任务状态查询、指标和追踪，目标是做成一个具备工程化能力的 Agent API 服务。

## 二、技术栈总览

这个项目的技术栈是非常适合面试展开的，因为它不止一门语言、也不止一个中间件。

- 后端框架：Go 1.22 + Fiber
- 数据库：MySQL
- 缓存与会话短期记忆：Redis
- 异步消息队列：RabbitMQ
- 向量检索预留：Milvus
- 大模型接入：OpenAI-compatible HTTP API
- 检索增强：本地 Hybrid Search + TypeScript RAG Bridge
- 认证授权：JWT
- 可观测性：Prometheus + OpenTelemetry
- 前端：嵌入式 HTML/CSS/JS 单页 UI
- 部署：Docker Compose

可以把这个项目理解为“一个有 Agent 思路的后端平台骨架”，它覆盖了后端开发里非常典型的工程问题：接口设计、缓存、异步任务、检索、模型调用、可观测性、部署和降级策略。

## 三、项目架构怎么讲

### 1. 启动流程

服务入口在 `cmd/api/main.go`。启动时先读取环境变量配置，然后初始化日志、Prometheus 指标、OpenTelemetry tracer，再按顺序初始化 MySQL、Redis、RabbitMQ、模型网关和 Service 层，最后创建 Fiber 服务器并注册路由。

这个启动流程有一个很重要的面试亮点：它不是“所有依赖失败就直接 panic 退出”，而是做了明显的降级设计。

- MySQL 初始化失败，服务仍然能启动，只是任务持久化和检索日志能力会退化。
- Redis 不可用，缓存、限流、会话短期记忆会退化，但 HTTP 接口还可以跑。
- RabbitMQ 不可用，异步任务能力退化，但 ingest 仍然可以走本地 goroutine 兜底。
- 模型不可用，项目仍然能返回固定兜底文案，而不是直接 500。

这说明你在设计时考虑了“服务可用性优先”和“核心能力与增强能力解耦”。

### 2. 分层设计

这个项目的分层比较清晰，可以按下面的口径讲：

- `internal/http` 是接口层，负责路由、中间件和响应格式。
- `internal/service` 是业务层，负责 chat、search、ingest、task 的业务编排。
- `internal/store/mysql` 是持久化层，封装 MySQL repository。
- `internal/cache/redis` 是缓存和会话记忆层。
- `internal/mq/rabbitmq` 是消息队列层。
- `internal/modelgateway` 是模型网关层，对接 OpenAI-compatible 接口。
- `internal/retrieval` 是检索层，负责 query 清洗、融合打分、本地 Hybrid 检索和 TS 桥接。
- `internal/obs` 是可观测性模块。

如果面试官追问“为什么要分层”，你可以答：这样做主要是为了职责单一和依赖倒置。HTTP 层只负责协议转换，不直接写业务逻辑；Service 层做业务编排，但不关心底层具体是 MySQL 还是别的存储；这样后续替换中间件或者补充真实 Milvus/Embedding 实现时，上层改动会更小。

## 四、核心功能链路怎么讲

### 1. /v1/chat 链路

`/v1/chat` 是整个项目最核心的接口。它的链路不是简单地把用户问题转发给模型，而是有一套完整编排：

第一步，校验请求参数，要求有 `tenant_id`、`session_id`、`user_id` 和 `message`。

第二步，对用户问题做 query 清洗和扩展。当前实现是启发式改写，后续可以替换成 LLM query rewrite。

第三步，从 Redis 中加载当前会话最近 N 条消息，作为短期会话记忆。这里用了 `LPUSH + LTRIM + EXPIRE` 的组合，保证只保留最近若干条消息，并通过 TTL 自动清理。

第四步，执行 RAG 检索。检索策略有优先级：

- 如果用户传了 `root_dir`，优先扫描本地目录，走 TypeScript RAG bridge。
- 如果有租户导入的数据，则优先从 MySQL 的 `chunks` 表做本地 Hybrid 检索。
- 如果 TS bridge 可用但没有显式 root_dir，则走默认目标目录检索。
- 如果上述都失败，退化成占位命中结果，保证接口契约稳定。

第五步，根据模型上下文窗口，对历史消息做压缩。这个地方是项目里比较能体现思考深度的点：不是盲目把所有历史消息都发给模型，而是先估算 token 预算，再优先保留最近消息，超出的旧消息交给模型做摘要，然后把摘要作为一条 system memory 回灌。

第六步，优先走 function-calling 模式。模型先决定是否调用 `search_codebase` 工具，如果调用，后端执行真实检索，把工具结果再回传给模型，让模型基于检索结果生成最终答案。

第七步，如果 function-calling 失败，就降级为普通 prompt 模式，把检索得到的上下文拼进 prompt 里，直接请求模型回答。

第八步，回答完成后，把本轮 user 和 assistant 消息追加进 Redis 会话记忆；如果 MySQL 可用，还会记录 retrieval log，便于后续分析召回情况。

这条链路很好讲，因为它体现了 Agent 服务的几个关键能力：会话记忆、检索增强、工具调用、上下文压缩、降级兜底。

### 2. /v1/search 链路

`/v1/search` 是独立检索接口，适合单独调试 RAG。它会先校验参数，然后按 `tenant_id + top_k + query + root_dir hash` 构造 Redis 缓存 key，如果命中缓存直接返回。缓存未命中时，调用 `buildRAGMaterials` 执行真实检索，再把结果写回 Redis，TTL 当前是 2 分钟。

这个接口的价值在于，它把“检索”和“生成”拆开了。面试时你可以说：我把 search 单独做成接口，主要是为了方便调试召回链路、验证检索质量，也便于将来做离线评测和可观测性分析。

### 3. /v1/ingest 链路

`/v1/ingest` 对应导入能力，是项目异步化设计的核心体现。

调用 ingest 时，服务会先生成一个 `task_id`，然后把任务记录写入 MySQL 的 `tasks` 表，状态初始为 `pending`。随后如果 RabbitMQ 可用，就把任务消息投递到 `tasks` 队列；如果 MQ 不可用，则用 goroutine 在本地异步执行同一套处理逻辑。

消费端 `StartTaskConsumer` 启动后会持续消费 MQ 消息。每条消息处理时，先把任务状态改为 `running`，再按 `type` 分发给具体处理函数。当前主要实现的是 `ingest`。

`handleIngestTask` 的流程是：

- 解析消息里的 ingest 请求
- 根据 `source_type` 读取文本内容，支持 `text` 和 `file`
- 计算 checksum，创建 document 记录
- 把文本按 rune 长度切 chunk，并带 overlap
- 估算 token_count
- 用事务“先删后插”重建 chunks
- 更新 document 状态为 `ready`
- 最终把 task 状态改为 `success` 或 `failed`

这里能讲的点很多：异步任务解耦、任务状态机、幂等设计的初步思路、文本 chunking 策略、事务保证一致性。

### 4. /v1/tasks/:id 链路

这个接口是给异步流程做状态查询的。前端或调用方拿到 `task_id` 后，可以轮询查询任务状态。后端从 `tasks` 表里读取状态，并把文本状态映射成粗粒度进度，比如 pending=10，running=50，success/failed=100。

这可以拿来回答“异步任务用户怎么感知进度”的问题。

## 五、检索模块怎么讲

这个项目的检索模块是面试里最值得展开的部分之一，因为它不是只会说“我接了个向量库”。

当前检索有两条路：

第一条路是本地 Hybrid Search。它从 MySQL 的 `chunks` 表读取某个租户的 chunk 数据，在内存里构建一个轻量级 Hybrid 索引：

- BM25：基于词频、文档频率和文档长度计算传统文本相关性
- Dense 近似：当前没有直接接入真实 embedding，而是用哈希向量模拟 dense 检索分支
- Query Coverage：统计一个 doc 被多少个 query variant 命中
- Path Boost：如果 query 里提到了路径片段或文件名，则给相应文档加分

最后按固定融合公式打分：

`final = 0.45*bm25_norm + 0.30*dense_norm + 0.15*query_coverage + 0.10*path_boost`

第二条路是 TS RAG bridge。由于你还有一个 TypeScript 版 agent，这里通过 Node 子进程调用 `scripts/ts_rag_bridge.mjs`，复用已有的 TS 检索模块。Go 服务把参数通过 stdin 传给脚本，脚本扫描目标目录、调用 TS 的 `buildRagData`，再把命中结果和拼装上下文回传给 Go。

这条桥接路线非常适合面试里讲“渐进式重构”和“跨语言复用”。你可以说：我没有一开始就全部重写检索，而是让 Go 服务先通过 bridge 复用已有 TypeScript RAG 能力，保证功能连续可用；同时在 Go 里实现本地 Hybrid 检索，逐步替换为纯 Go 版本。

## 六、数据库与缓存设计怎么讲

### 1. MySQL

MySQL 里现在已经有这些核心表：

- `users`
- `sessions`
- `messages`
- `documents`
- `chunks`
- `retrieval_logs`
- `tasks`

其中当前项目实际用得比较多的是 `documents`、`chunks`、`retrieval_logs` 和 `tasks`。如果面试官问“为什么表这么设计”，你可以说：

- `documents` 存文档级元信息，比如 source_type、source_uri、checksum、status
- `chunks` 存切分后的文本块，是检索的核心数据源
- `tasks` 存异步任务状态，用于 ingest 跟踪
- `retrieval_logs` 存每次检索的 query、topK、命中结果等，为后续评估检索质量提供基础

这个 schema 虽然还不算完整，但已经体现了从“对话层”到“检索层”再到“异步处理层”的数据结构设计。

### 2. Redis

Redis 这里做了三件事：

- 搜索缓存
- 固定窗口限流
- 会话短期记忆

固定窗口限流实现简单，但有秒边界突刺问题。这个反而是你面试时的加分点，因为你可以主动讲缺点：当前限流实现是按秒分桶的 fixed window，优点是实现简单，缺点是边界时刻可能产生瞬时突刺；如果线上流量更复杂，我会改成 sliding window 或 token bucket。

会话短期记忆用 Redis List 存储最近消息，优点是实现简单、读写快、TTL 管理方便，适合会话上下文这种短生命周期数据。

## 七、模型网关和上下文压缩怎么讲

模型网关层统一封装了 OpenAI-compatible `/chat/completions` 接口，这意味着后续切换 provider 时不需要改业务层逻辑。网关还能探测 `/models` 接口中的上下文窗口配置，探测不到再按模型名猜测一个保守值。

这里很适合回答“为什么要做模型网关而不是业务层直接调 HTTP”。答案是：一方面是隔离第三方接口差异，另一方面是统一处理超时、鉴权、模型能力探测和未来多模型切换。

上下文压缩这块是项目里的一个亮点。很多项目会把全部对话历史直接拼进去，导致 token 爆炸和成本上升。这个项目的做法是：

- 先估算系统提示词、当前 query、RAG context、预留回答的 token 开销
- 动态算出历史消息预算
- 优先保留最近消息
- 对更早的消息做摘要
- 把摘要作为系统记忆补回去

面试官如果问“为什么要这样做”，你就说：因为历史消息和 RAG context 都会占用上下文窗口，如果不做预算控制，很容易导致模型截断或者成本失控；这个策略的核心目标是用更稳定的 token 预算保留高价值信息。

## 八、工程亮点和可讲价值

这个项目最适合你在面试里强调的亮点，不是“我用了很多中间件”，而是下面这些能力：

第一，完整链路意识。不是写了几个接口，而是把启动、配置、接口、中间件、存储、缓存、异步任务、检索、模型、监控和部署串起来了。

第二，降级设计。MySQL、Redis、RabbitMQ、模型、TS bridge 任意一环出问题，系统都不是直接挂，而是尽量退化到仍可用的模式。

第三，Agent 思维。chat 不是纯 prompt，而是带 tool calling、检索增强、会话短期记忆和上下文压缩。

第四，异步任务架构。ingest 不是同步阻塞请求，而是任务化处理，并支持 `/tasks` 查询状态。

第五，可观测性意识。已经有 `/metrics`、Prometheus 指标、基础 tracing provider 和 request_id。

第六，跨语言迁移能力。通过 TS bridge 复用旧能力，让 Go 重构不是“一刀切推倒重来”，而是逐步演进。

## 九、项目当前短板怎么讲

面试时不要把项目吹成“已经完美生产可用”，那样一追问就露馅。更好的讲法是：核心闭环已经打通，但还有几个明确的待完善点。

- RabbitMQ 目前只有最小消费骨架，重试、死信队列、退避策略还没补全。
- Milvus 只是占位客户端，真实 embedding 写入和向量查询尚未落地。
- 当前 Hybrid dense 分支是哈希向量近似，不是真实 embedding。
- JWT 目前只有浏览器 bootstrap token，没有完整登录签发流程。
- tracing 目前是最小骨架，没有把 HTTP、DB、MQ、LLM 请求串成完整 spans。
- MySQL 里 sessions/messages 表还没有完整 CRUD 接口。
- 检索日志有了，但还没形成离线评测和质量看板。

这种回答会让面试官觉得你对项目成熟度有判断，不会乱吹。

## 十、面试官可能会问的题目与答题思路

### 1. 你为什么选 Fiber，而不是 Gin 或原生 net/http？

答题思路：我选 Fiber 主要是看重它性能和开发效率的平衡。它的路由、中间件模型和 Express 比较像，写起来比较快，同时基于 fasthttp，有不错的吞吐能力。对这个项目来说，需求重点是快速搭建 API、路由分组、中间件链和静态 UI 服务，Fiber 很合适。当然如果团队统一使用 Gin，也可以替换，因为我业务逻辑都放在 service 层，HTTP 框架并没有和业务强耦合。

### 2. 你的项目为什么要分 handler 和 service？

答题思路：handler 只做 HTTP 协议层工作，比如解析 JSON、返回状态码和统一响应体；service 负责业务编排，比如 chat 会话记忆、检索、模型调用、日志记录。这样做的好处是职责清晰，便于测试，也便于后续把同一套 service 逻辑复用到别的协议层，比如 gRPC 或异步任务。

### 3. 这个项目的 chat 路由和普通聊天接口有什么区别？

答题思路：我的 chat 不是“用户输入一句话，模型回一句话”这么简单。它在调用模型之前会做 query 清洗、读取 Redis 历史记忆、执行检索、压缩上下文，然后优先尝试 function-calling，让模型自主决定要不要调 `search_codebase` 工具。如果工具链失败，再降级到普通 prompt 模式。这更接近 Agent 服务，而不是普通 chat wrapper。

### 4. 你为什么要做 function-calling，而不是直接把检索结果拼进 prompt？

答题思路：直接拼 prompt 的问题是，检索时机和检索参数都由后端写死了；function-calling 的好处是让模型自己决定什么时候检索、用什么 query 检索、返回多少结果，这更符合 Agent 的“工具使用”思路。同时我也保留了普通 prompt 兜底，避免 provider 不支持 tools 时整个功能失效。

### 5. Redis 会话记忆为什么用 List，而不是 Hash 或直接落 MySQL？

答题思路：会话短期记忆的特点是高频读写、只关心最近几条、生命周期短。Redis List 很适合做这种最近 N 条消息的窗口缓存，`LPUSH + LTRIM + EXPIRE` 组合实现简单。MySQL 更适合做持久化历史，但不适合每轮聊天都高频读写短期上下文。

### 6. 你这个限流是怎么做的？有什么问题？

答题思路：现在是 Redis 固定窗口限流。key 维度是路径加 user_id，没有 user_id 就退化为 IP。每秒一个桶，通过 `INCR + EXPIRE` 计数。这个方案优点是简单、成本低，缺点是窗口边界会有 burst spike。如果要线上更精细，我会改成滑动窗口或者 token bucket。

### 7. 你的 ingest 为什么设计成异步？

答题思路：因为导入文档、读取文件、切 chunk、后续如果加 embedding 生成，这些都属于耗时操作。如果同步执行会拖慢接口响应，也不利于扩展。异步设计可以让接口快速返回 `task_id`，再由消费者后台处理，用户通过 `/tasks/:id` 轮询状态。这是典型的“削峰、解耦、提升用户体验”的思路。

### 8. 任务状态为什么要落 MySQL，而不是只放 RabbitMQ？

答题思路：MQ 只负责传递消息，不适合做任务状态查询。用户需要一个稳定可查询的状态源，所以我要把任务记录持久化到 MySQL 里。这样即使消费者重启，任务状态也还在，前端也能通过 `task_id` 查进度。

### 9. 你的 chunk 切分策略是什么？为什么要 overlap？

答题思路：当前是按 rune 长度切块，并保留一定 overlap。这样做的原因是文档语义可能跨越 chunk 边界，如果完全硬切，边界上下文容易丢失；保留 overlap 可以提高召回后的可读性和连续性。当然这只是简化实现，后续可以升级成按段落、标题、代码块、语义边界进行 smarter chunking。

### 10. 你为什么没有直接上真实 embedding + Milvus？

答题思路：我这里是分阶段建设。先把检索接口、融合公式、数据结构和调用链路跑通，再逐步把 dense 分支替换成真实 embedding + Milvus。这样好处是系统可以先具备可演示能力，架构也先稳定下来。面试里我会明确说明：当前 dense 是近似实现，但接口和扩展位已经预留好了。

### 11. 你这个 Hybrid 检索为什么要做多路融合？

答题思路：因为单独 BM25 容易漏掉语义相近但词不完全匹配的内容，单独向量检索又可能忽略精确关键词，尤其是代码、路径、函数名、文件名这种强词法信号场景。所以我做了 BM25、dense、queryCoverage、pathBoost 的融合。对于代码检索来说，路径和标识符其实很重要，不能只依赖纯语义检索。

### 12. 你为什么要保留 retrieval_logs？

答题思路：检索系统如果没有日志和评估，很难知道召回质量好不好。保存 query、topK 和 hit_ids，后续可以分析哪些 query 命中差、不同融合权重效果如何，还能给离线评测和线上问题排查提供数据基础。

### 13. 这个项目是如何做可观测性的？

答题思路：我做了三层基础能力。第一层是 request_id，中间件会给每个请求生成唯一 id，方便串联日志和响应。第二层是 Prometheus 指标，统计请求数量和耗时。第三层是 OpenTelemetry tracer provider，虽然现在还是最小骨架，但扩展点已经放好了，后续可以把 HTTP、DB、MQ、LLM 调用都串成 span。

### 14. 你觉得这个项目最有工程价值的点是什么？

答题思路：我认为不是某一个技术点，而是它体现了一个服务从“能跑”到“像一个后端系统”的过程。比如依赖初始化与降级、统一响应体、鉴权、限流、异步化、检索、会话记忆、监控、部署，这些放在一起才是真正的工程价值。

### 15. 如果让你继续优化，你下一步做什么？

答题思路：我会优先补三件事。第一，完善异步任务的重试、死信队列和幂等机制。第二，把 dense 检索升级成真实 embedding + Milvus。第三，补 tracing span 和离线评测体系。因为这三件事能显著提升系统稳定性、检索效果和可观测性。

## 十一、你现在最该背哪些模块

如果你是为了手机上快速背面试内容，不要什么都平均发力，要按优先级来。

第一优先级：Go 基础和并发

- goroutine 和 channel
- context 的使用场景
- defer、panic、recover
- slice、map、interface、pointer
- 锁、并发安全、内存逃逸可以先背基础版

原因很简单：这是 Go 岗的地基，项目里也确实用了 goroutine、context、channel-like async 思维和 recover 中间件。

第二优先级：MySQL

- 索引原理、聚簇索引、回表、覆盖索引
- 事务、隔离级别、MVCC、幻读
- 慢查询优化
- 连接池和 SQL 执行过程

原因是你的项目里有明确的表结构、repository、事务插入 chunks、任务状态管理，这些非常容易被追问。

第三优先级：Redis

- 常见数据结构
- 缓存穿透、击穿、雪崩
- 分布式锁基础
- 过期策略和淘汰策略
- 限流实现方式

原因是你项目里 Redis 不只是“存个缓存”，还做了限流和会话记忆，这很好展开。

第四优先级：消息队列

- RabbitMQ 基础模型
- ack / nack
- 重试和死信队列
- 消息丢失、重复消费、顺序性、幂等

原因是你的 ingest 明确用了 MQ，这部分问到概率很高。

第五优先级：HTTP 和中间件

- JWT 原理
- 鉴权流程
- 幂等和状态码
- 中间件执行链
- RESTful 设计

第六优先级：RAG / LLM / Agent 基础

- 什么是 RAG
- 为什么要 chunk
- 为什么要 Hybrid Search
- function-calling 的意义
- 上下文窗口、token 控制、会话记忆

这部分是你的项目特色，不能只会说名词，至少要能把 chat 链路讲顺。

## 十二、临时抱佛脚的背诵建议

如果你准备时间不多，就按下面方式背。

第一天先背项目总述和架构链路。目标不是记住每个文件名，而是能 3 到 5 分钟完整讲出“这个项目是干什么的、接口有哪些、chat/search/ingest 怎么走、为什么要用 Redis/MQ/MySQL”。

第二天重点背 Redis、MySQL、RabbitMQ，因为这些最容易被从项目追问到底层原理。

第三天背 Go 并发、context、panic/recover、channel、锁，以及 Fiber 中间件机制。

第四天背 RAG、Hybrid Search、function-calling、上下文压缩。你不用背论文级别的理论，但至少要能说清楚为什么这么设计。

最后一天做模拟问答。你可以用下面这个顺序自己口述：

1. 先用 1 分钟介绍项目
2. 再用 3 分钟讲 chat 链路
3. 再用 2 分钟讲 ingest 异步任务
4. 再用 2 分钟讲 Redis 和 MySQL 的作用
5. 再用 2 分钟讲检索和 Agent 能力
6. 最后主动说当前短板和下一步优化

你要记住一个原则：面试不是背源码，而是讲“设计目标、核心链路、技术取舍、已做能力、未做能力”。只要这五件事你能讲顺，就已经比很多只会背八股但讲不出项目的人强很多。

## 十三、适合你当前项目的自我介绍模板

你可以这样说：

我最近主要做了一个 Go 版的 Agent 服务端，基于 Fiber 搭建，核心目标是把大模型问答做成一个后端系统，而不是单纯调用模型 API。项目里我实现了统一 API 契约、JWT 鉴权、Redis 限流和会话短期记忆、MySQL 持久化、RabbitMQ 异步 ingest、检索增强和模型网关。chat 链路里我做了会话历史压缩和 function-calling，优先让模型调检索工具，不支持时再降级成普通 prompt。整个服务还接了 Prometheus 指标和基础 tracing，并且通过 Docker Compose 把 MySQL、Redis、RabbitMQ、Milvus、Prometheus、Grafana、Jaeger 一起编排起来。这个项目让我比较完整地把 Go 后端、异步任务、缓存、检索和 Agent 能力串起来了。

这段最好背熟，因为它能同时把项目定位、技术栈和亮点全部带出来。
