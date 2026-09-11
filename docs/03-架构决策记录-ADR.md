# 架构决策记录（ADR）

> 记录 hq-push-gate 的关键技术决策，并解决原始调研记录（xuq.md）中遗留的矛盾点。
> 状态标记：已采纳 / 已否决 / 待定

## ADR-001 部署路线：生产底座缩容部署

- **状态**：已采纳（解决 xuq.md L1388 原型路线 vs L1468 终局架构的矛盾）
- **决策**：架构按生产级设计（Kafka+Flink+CK+Doris），部署从最小集群起步（3 broker、少量 TM），随规模加节点，不中途更换技术栈。
- **理由**：项目目标是论文/技术体现，架构演进本身是论文内容；中途换栈（Redis Stream→Kafka）会产生不可比的实验数据。
- **否决方案**：Redis Stream + DuckDB 轻量原型路线——保留为**本地开发最小依赖模式**（docker-compose 单机），不作为正式架构。
- **后果**：初期运维面大；通过 compose 一键起全链路缓解。

## ADR-002 时序存储：ClickHouse 存明细 + Doris 做查询层

- **状态**：已采纳（解决 xuq.md 单 Doris vs CK vs CK+Doris 的摇摆）
- **决策**：tick/K线原始数据写 ClickHouse（Replicated + Distributed，扛写入）；Doris 通过外表映射 CK，承载业务查询、统计、AI 筛选；Doris 自有表仅存告警事件/统计结果。
- **理由**：CK 写入吞吐强但 Join/并发弱；Doris 查询与 MySQL 协议对业务友好。组合是量化平台生产验证过的方案，且是论文中"存储分层"的体现点。
- **否决方案**：单 Doris（tick 高写入下压力风险）、TDengine/InfluxDB（多条件筛选弱）、DuckDB（嵌入式，无分布式）。

## ADR-003 入库链路：独立消费任务，与 Flink 解耦

- **状态**：已采纳（解决 xuq.md L343/L1407/L1569 三种入库说法并存的矛盾）
- **决策**：统一采用独立 ingest-worker 消费 Kafka 写 CK；**Flink 不直接承担入库**（Flink Sink 写 CK 仅保留为开发期简化开关）。
- **理由**：OLAP 故障/慢写不触碰实时链路；ingest 可独立重启、可重放 Kafka 补数；预警（主业务）与入库（次业务）故障域隔离。
- **后果**：多一个服务；换来链路隔离与可重放性。

## ADR-004 规则同步：Kafka 广播为主 + Debezium 对账兜底

- **状态**：已采纳（解决 xuq.md 双通道并存未定义主备的问题）
- **决策**：规则变更主通道 = 业务事务写入 MySQL 与 `outbox_event`，由 outbox-publisher 发布 `rule_bcast`（带 version 单调递增）；Debezium 消费 binlog **不直接下发**，只做周期（1min）对账：比对 MySQL 生效规则与 Flink 已加载版本，差异自动重发广播。
- **理由**：双通道同时下发会导致乱序与重复；对账模式保证最终一致且主从关系明确（Kafka 为主，CDC 为纠偏）。
- **后果**：极短窗口内规则生效延迟（≤1min 容忍）；实现简单可靠。

## ADR-005 数据源：AKShare 历史灌数 + 自研模拟 tick 发生器

- **状态**：已采纳（非商业定位）
- **决策**：历史K线用 AKShare 批量拉取灌入 CK；实时链路使用自研模拟 tick 发生器（可配置：标的数、tick 速率、脉冲模式、价格游走模型）；hq-gateway 按统一接口对接，未来可无缝替换为商业源。
- **理由**：零版权成本；模拟源压力可编程控制，是论文压测实验的可控变量（比真实行情可复现）。
- **后果**：论文中需声明数据为模拟；架构上行情源适配层保证替换成本最低。

## ADR-006 推送渠道：自建 WS 集群为核心，其余渠道适配器化

- **状态**：已采纳
- **决策**：PC/H5 双端统一走自建 ws-gateway 集群（WSS）；邮件/公众号/Webhook 实现为 notify 的可选适配器插件，默认关闭。
- **理由**：非商业定位下公众号非必需；自建 WS 集群是论文高并发（10w~100w 连接）的核心验证对象。
- **后果**：减少外部依赖；公众号模板消息的金融类目限制问题不再相关。

## ADR-007 API 网关：独立 APISIX 集群

- **状态**：已采纳
- **决策**：部署独立 APISIX（≥2 节点）作为唯一公网入口，承担 JWT 校验、限流、WSS 透传、路由。
- **理由**：限流/鉴权/观测是论文高并发实验的测量点；业务服务内置鉴权会使限流策略无法统一观测。
- **否决方案**：原型期业务内置鉴权（损失观测点，且后期迁移成本高）。

## ADR-008 前端仓库：pnpm monorepo 双端 + 共享 SDK

- **状态**：已采纳
- **决策**：`apps/pc-web`（React19 + AntD Pro + klinecharts）、`apps/h5`（React19 + AntD Mobile + klinecharts）、`packages/api-client`（REST+WSS 封装，含重连/补拉/幂等去重逻辑）、`packages/shared`（类型与常量）。
- **理由**：PC/H5 交互差异大（多栏 vs 单列、完整管理 vs 移动轻量），双应用共享 SDK 与类型，避免逻辑重复实现两遍。
- **否决方案**：单响应式站点（推送策略、页面结构差异大，强行统一劣化两端体验）。

## ADR-009 实时行情扇出：quote-push 独立服务，不进 Flink

- **状态**：已采纳（对 xuq.md "Flink 更新 Redis 快照"的修正）
- **决策**：新增 quote-push（Go）服务：消费 `tick_raw`，内存维护最新价，合并窗口刷 Redis 并按 `gateway_slot` 路由到固定 `ws_push`；Flink 专注有状态计算（K线/指标/规则），仅输出 K线闭合与告警事件。
- **注记（2026-09-10）**：合并窗口设计初值 500ms，实现默认 **200ms**（`QUOTE_PUSH_MERGE_WINDOW` 可调，联调/smoke 实测值）；窗口只影响行情快照与扇出批量，不在告警链路上。U3 压测按扇出效率与 Redis 写放大复评是否上调至 500ms，调整走 env/ADR 注记。
- **理由**：快照扇出是无状态 IO 密集型工作，Go 处理更高效；行情展示流与预警计算流故障域隔离；避免 Flink 承担高频外部写。
- **后果**：tick 被 Flink 与 quote-push 两组消费（Kafka 广播消费组，正常设计）。

## ADR-010 AI 筛选：LLM 仅做 NL→条件翻译，执行全部下推 Doris

- **状态**：已采纳（可选模块，M3）
- **决策**：LLM 输出必须符合 JSON Schema（市场/周期/指标/阈值枚举白名单），服务端校验后编译为受限 Doris SQL 执行；高频条件 Redis 缓存；结果页附免责声明。
- **理由**：杜绝大模型幻觉编造标的；可测试、可复现。
- **否决方案**：Text-to-SQL 自由生成（注入与幻觉风险）。

## ADR-011 序列化：内部 Protobuf，对外 REST/WS 用 JSON

- **状态**：已采纳
- **决策**：Kafka 全链路 Protobuf（tick ~40B）；对外 REST JSON；WS 下行 JSON（前端友好），超大批量帧场景预留二进制帧扩展位。

## ADR-012 压测与混沌为内建能力

- **状态**：已采纳（论文核心）
- **决策**：模拟 tick 发生器、WS 客户端模拟器（可开 10w 连接）、批量用户/规则生成器、Chaos Mesh 故障注入全部作为平台内置模块（管理后台可控），而非外部脚本。
- **理由**：实验可复现、可编排；"压测即服务"本身是工程亮点。

## ADR-013 前端技术栈：纯 H5（React SPA），不用 uni-app / Taro

- **状态**：已采纳（细化 ADR-008 中 H5 端的框架选型）
- **决策**：H5 采用 **React 19.x + Vite 6.x + Ant Design Mobile 5.x + klinecharts 9** 的纯 Web SPA；不引入 uni-app / Taro 等跨端框架。
- **理由**：
  1. 需求只有 H5（移动浏览器），无小程序/原生 App 目标——跨端抽象层是纯成本无收益；
  2. 与 PC 端同栈（React+TS），`packages/api-client`（重连/退避/补拉/幂等）与 `packages/shared`（类型）直接复用，hooks 与状态逻辑可共享；
  3. **WebSocket 长连接是核心场景**：纯 H5 浏览器 WS 无限制；小程序侧 `wx.connectSocket` 有并发连接数限制且金融行情类目审核严格；
  4. 无运行时抽象层：包体、调试、长连接性能都是最优路径。
- **否决方案**：uni-app（Vue 栈割裂、无多端诉求）、Taro 4（H5 是次等编译目标；将来做小程序时 api-client 纯 TS 包可直接复用再建 `apps/mp-weixin`）、Vue3+Vant（双栈维护成本 > 包体收益）。
- **H5 应用内架构**：Vite（代码分割 + PWA 可选）+ react-router 懒加载（实际 v7）；zustand（WS 流式行情），TanStack Query v5（REST 缓存）为规划态未引入；react-virtuoso 长列表；弱网/省电策略（指数退避+抖动、重连先 REST 快照再续订、Page Visibility 退订）全部下沉到 api-client。
- **后果**：若未来新增微信小程序入口，评估 Taro 4 单独建应用复用 api-client，不动 H5。

## ADR-014 百万 WebSocket 连接：Go 连接层 + 集群化 + 分级推送

- **状态**：已采纳（目标从 10w 提升到 100w 并发连接）
- **决策**：
  1. 连接层 **Go**（实际 gorilla/websocket；"标准库 net 深度调优/gnet"为目标态可选），单实例按 20w 连接规划，百万连接目标为 6 个工作实例+2 个故障冗余实例；Netty 为备选（单机密度优先时切换，JVM/ZGC 调优成本换密度）；
  2. **smart-socket 否决**：能力可达成，但社区规模、生产案例、论文可引用性均弱于 Netty/Go；
  3. **分级推送**：目标态为客户端可见性三级（QUOTE_FULL/QUOTE_SLOW/ALERT_ONLY，U3+ 扩展），**现行实现为套餐权益差频**（free 10s / vip 3s，ADR-040/041）——分级削峰目标不变，维度演进为套餐档位；不做分级扇出需 40Gbps egress（不可行），分级后 ≤10Gbps；
  4. 公网 TLS 在 APISIX 终止，ws-gateway 复核 WS token；恒速准入（5w conn/s，目标态）+ 客户端抖动退避对抗重连风暴。
- **理由**：百万连接的本质是内核 fd/内存预算 + 集群架构 + 出口带宽问题，不是语言之争（Go/Netty 均有 C1M 实战先例）；选 Go 与团队栈和既有推送服务一致。
- **实验锚点**：S3 分级验证 10w→50w→100w；记录单实例连接-内存曲线与集群 egress 曲线。

## ADR-015 API 网关：APISIX（否决 Spring Cloud Gateway / 自研 Go）

- **状态**：已采纳（细化 ADR-007，回应"网关 Java 还是 Go"）
- **决策**：公网统一入口采用 **APISIX 3.x**（Nginx/OpenResty 内核 + etcd 热配置）。三个网关角色分工：APISIX=边缘静态职责（JWT 校验/限流/路由/HTTPS），ws-gateway=WSS 长连接（Go 自研，ADR-014），hq-gateway=行情源接入（Go 自研，ADR-005）。
- **对比**：

| 方案 | 内核 | 吞吐 | 否决/采纳原因 |
|---|---|---|---|
| **APISIX** | Nginx+LuaJIT | 10w~100w QPS/节点 | **采纳**：Nginx 级性能；插件生态全（jwt-auth/limit-req/prometheus）；etcd 热更新；Apache 顶级项目论文可引用；中文社区强 |
| Spring Cloud Gateway | Java/Netty | 数万 QPS/节点 | **否决**："Java 网关"是伪需求——边缘网关无业务逻辑，Java 生态优势兑现不了，吞吐低一个量级 + JVM 内存开销；且与团队"重业务用 Java"的分工原则矛盾 |
| Kong | Nginx+Lua/Go | 同级 | 否决：能力接近，国内案例与中文资料少于 APISIX |
| Traefik | Go | 中 | 否决：云原生路由强，但限流/鉴权插件生态弱，金融类边缘策略不够 |
| 自研 Go 网关 | Go | 可做 | 否决：限流/热配置/可观测全要自造轮子，收益为零 |

- **WSS 特殊处理**：APISIX 终止公网 TLS 并代理 HTTP Upgrade 到 ws-gateway；ws-gateway 复核 token 和业务授权，避免把 L4 透传与应用层 JWT 校验混为一谈。
- **理由**：边缘网关选型逻辑是"成熟插件 + 性能内核 + 热配置"，不是语言站队；Java/Go 留给自研的领域网关（ws-gateway/hq-gateway），那才是语言能力有差异的地方。

## ADR-016 OLAP 层：Apache Doris（主）+ ClickHouse（明细），StarRocks 为升级备选

- **状态**：已采纳（细化 ADR-002，回应 Doris 同类产品疑问）
- **决策**：维持 **CK 存 tick/kline 明细 + Doris 外表查询层**；Doris 选 **Apache Doris 3.x LTS**；**若 S4 实验多表 Join P99 > 1s 则切换 Doris 4.1.x 或 StarRocks**（MySQL 协议兼容，业务代码零改动，仅换部署）。
- **同类对比**：

| 产品 | 定位 | 判定 | 原因 |
|---|---|---|---|
| **Apache Doris 3.x LTS** | MPP 实时 OLAP | **主选** | MySQL 协议、Join/并发强、外表映射 CK、社区中立（Apache 顶级项目）、论文引用规范 |
| StarRocks | Doris 同源商业开源分支 | 备选 | 向量化/多表 Join 更强、存算分离成熟；但版本节奏快、兼容 MySQL 协议可平替——作为性能不达标时的逃生门 |
| Apache Druid | 实时摄入+预聚合 | 否决 | 运维重（ZK+深存储+historical/broker 多角色）；SQL/Join 弱；更适合日志指标监控 |
| DolphinDB | 量化一体（时序+因子+脚本） | 否决 | 量化场景契合度高，但**核心闭源商业授权**，与开源论文定位冲突 |
| TDengine / InfluxDB | 时序库（设备监控模型） | 否决 | 多条件筛选、复杂 Join、AI 选股批量扫描弱（xuq.md 已论证）；tag 模型不适配金融多维分析 |
| Greenplum | 传统 MPP 数仓 | 否决 | 高频小批量实时摄入弱，适合 T+1 离线批处理 |

- **职责边界**（重申）：明细写入压力给 CK（ReplacingMergeTree 去重 + 分区分片 + 物化视图预聚合）；Doris 只做查询/筛选/统计/AI 执行引擎——**两层都不可被单一产品替代**（单 CK Join/并发弱；单 Doris 高频写入明细风险）。

## ADR-017 全栈技术选型冻结总表（V2.2）

- **状态**：已采纳（回答"所有技术栈是否选齐"——是，全部冻结，M1 起不再摇摆）

| 层 | 组件 | 技术栈/版本 | 决策 |
|---|---|---|---|
| 边缘网关 | APISIX | 3.x + etcd | ADR-015 |
| 行情接入 | hq-gateway | **Go 1.25.x** +标准库 WS 客户端；多源容灾 | ADR-005 |
| 流计算 | flink-job | **Java 21 LTS + Flink 1.20.x** + RocksDB 状态后端 + TA-Lib(JNI) | ADR-003/009 |
| 快照扇出 | quote-push | **Go 1.25.x**（合并窗口默认 200ms 可调、订阅聚合过滤；见 ADR-009 注记） | ADR-009 |
| 连接推送 | ws-gateway | **Go 1.25.x**（单实例按 20w 连接规划；gorilla/websocket，gnet 可选） | ADR-014/018 |
| 告警分发 | notify | **Go 1.25.x**（重试/死信/适配器） | ADR-006 |
| 入库 | ingest-worker | **Go 1.25.x**（攒批 5k 行/1s 写 CK） | ADR-003 |
| 业务服务 | biz-service | **Java 21 LTS + Spring Boot 3.x** + MyBatis-Plus + MySQL Connector | ADR-004 |
| AI 筛选(可选) | ai-query | **Python 3.12 + FastAPI** + LLM SDK + JSON Schema 校验 | ADR-010 |
| 补数(可选) | 补数任务 | **Python + AKShare/Tushare 降级** | ADR-005 |
| 消息总线 | Kafka | **4.1.x（KRaft）** RF=3 | ADR-001 |
| 时序明细 | ClickHouse | **26.3.x LTS**，2 分片×2 副本 | ADR-002/016 |
| 查询 OLAP | Apache Doris | **3.x LTS**（备选 4.1.x/StarRocks） | ADR-016 |
| 业务库 | MySQL | 8.0 主从半同步 + Debezium 对账 | ADR-004 |
| 缓存 | Redis | **7.4.x** Cluster 3主3从起 | — |
| PC 前端 | pc-web | **React 19.x + TS 5 + Vite 6.x + AntD 6.x + klinecharts 10** | ADR-008 |
| H5 前端 | h5 | **React 19.x + TS 5 + Vite 6.x + AntD Mobile 5.x**（klinecharts 随 H5 K 线页引入，规划态） | ADR-013 |
| 前端共享 | api-client/shared | **TS 5 + zustand**（TanStack Query v5 为规划态未引入；REST 缓存现经 services 层 + zustand 承担） | ADR-008/013 |
| 压测 | bench/* | Go（tick 发生器/WS 模拟器）+ k6/wrk2 | ADR-012 |
| 编排 | K8s | 1.31+ + Strimzi/Flink/Altinity/Doris Operator + Chaos Mesh | — |
| 观测 | 可观测 | Prometheus + Grafana + Loki + OTel Collector + Tempo + Pyroscope | — |

- **语言分工原则**（最终版）：**Go=IO/连接密集**（网关/推送/入库/扇出）、**Java=有状态流计算+重业务事务**（Flink/SpringBoot）、**Python=AI 与数据脚本**、**TS/React=双端前端**；任何"统一语言"提案默认否决。
- **变更控制**：上表冻结后变更需新增 ADR 记录理由（尤其 StarRocks 切换、Netty 切换两个预留逃生门）。

### ADR-031 运行时与中间件版本基线

- **状态**：已采纳
- **决策**：最终实施基线采用 Go 1.25.x、Java 21 LTS、Flink 1.20.x、Kafka 4.1.x、ClickHouse 26.3.x LTS、Doris 3.x LTS、Redis 7.4.x、React 19.x、Node.js 24.x LTS。Flink 2.x、Doris 4.1.x、Redis 8.x 不作为首发基线，只有完成兼容矩阵、回归压测、状态恢复和 Operator 验证后，才能新增 ADR 升级。
- **理由**：原基线中的 Go 1.22、Flink 1.18、Kafka 3.7、ClickHouse 24.x、Doris 2.1 和 React 18 对新项目偏旧；但直接追逐所有最新大版本会增加状态恢复、客户端、Operator 和前端生态风险。采用成熟运行时与保守大版本组合，兼顾维护周期、性能和论文复现性。
- **版本规则**：镜像和构建文件固定 major.minor；安全 patch 可自动升级；任何 major/minor 升级必须先通过兼容矩阵、契约测试、基准压测和故障恢复验证。

## V2.1 冻结补充 ADR

### ADR-018 验收资源分层

- **状态**：已采纳
- **决策**：`dev` 只验收功能闭环；`benchmark-small`（10~15 台 8C16G）验收 10 万 tick/s、10 万连接和 100 万规则；`benchmark-large` 使用独立扩展资源验收百万连接。
- **理由**：避免把小规格实验集群误写成百万连接实测环境，同时保留连接层的水平扩展目标。

### ADR-019 告警投递可靠性

- **状态**：已采纳
- **决策**：使用 `event_id` 表示全局命中事件，使用 `delivery_id` 表示用户投递；alert-inbox 持久化投递状态，客户端通过 `cursor` 补拉并发送 ACK。
- **理由**：at-least-once 只能提供至少一次传输，必须增加持久化状态、补拉游标和客户端确认才能验证保留窗口内可达。

### ADR-020 固定推送 Topic

- **状态**：已采纳
- **决策**：统一使用固定 64 分区的 `ws_push` Topic，通过 `gateway_slot` 路由；实例通过 Redis 租约持有槽位并手动消费对应分区，不按 ws-gateway 实例动态创建 Topic。
- **理由**：HPA 扩缩容不应引入 Topic 生命周期、元数据刷新和消息迁移问题。

### ADR-021 规则广播与用户投递解耦

- **状态**：已采纳
- **决策**：RuleMsg 不携带 `user_ids`；Flink 只做规则命中，notify 根据 MySQL/Redis 订阅索引展开用户投递。
- **理由**：避免百万规则广播消息和 Flink Broadcast State 保存重复用户列表，降低规则计算与投递耦合。

### ADR-022 公网 TLS 与 WS 鉴权边界

- **状态**：已采纳
- **决策**：公网 TLS 在 APISIX 终止并执行边缘 JWT 校验；ws-gateway 在 Upgrade 阶段复核 token/reconnect ticket 并执行业务授权。
- **理由**：固定应用层鉴权责任，避免 L4 透传时 APISIX 无法读取加密 HTTP Upgrade 请求却仍声称完成 JWT 校验。

### ADR-023 MySQL 扩展路线

- **状态**：已采纳
- **决策**：U0~U3 不做水平分库分表；按单库、主从/高可用、读写分离、告警表时间分区和归档逐级扩展。U4 以后先按职责垂直拆分，只有实测出现单表容量、写入、锁竞争或恢复时间瓶颈时，才通过新增 ADR 引入按 `user_id` 路由的水平分片。
- **理由**：MySQL 不在行情和 WS 热路径；过早分片会增加规则事务、全局唯一 ID、Debezium CDC、跨库查询和运维复杂度，当前规模没有收益。

### ADR-024 可观测性主平台

- **状态**：已采纳
- **决策**：采用 Prometheus + Grafana + Loki + OpenTelemetry Collector + Tempo + Go pprof/Pyroscope；SkyWalking 不作为默认平台，仅在已有 SkyWalking 运维能力时替代 Tempo。
- **注记（实现现状）**：已落地 Prometheus + Grafana + Alertmanager；Loki/Tempo/Pyroscope 为 U2+ 分期项（docs/08 §7.1.3），trace 现以 trace_id 贯穿日志实现（HTTP/Kafka/WS 透传）。
- **理由**：统一覆盖 Go、Java、Python 和 Kafka 链路，避免同时维护两套 APM/Trace 平台；指标、日志、链路和函数级火焰图分别负责不同问题。

### ADR-025 中间件分阶段拓扑与消息总线

- **状态**：已采纳
- **决策**：Kafka 作为唯一实时事件总线；U0~U1 允许 Kafka、Flink、Redis、MySQL、ClickHouse 单实例或缩容部署，U2 开始启用小规模 HA，U3 进入分布式压测基线。RabbitMQ 当前不引入，Elasticsearch 当前不引入。
- **理由**：Kafka 的分区、消费组、保留和 offset 重放与 tick 流、Flink、入库和多路扇出匹配；RabbitMQ 更适合任务型消息；当前查询和日志场景已有 MySQL/Redis、ClickHouse、Doris、Loki 覆盖，提前引入 ES 只会增加运维和数据同步成本。

### ADR-026 业务事件采用 MySQL Outbox

- **状态**：已采纳
- **决策**：规则、订阅等业务写入与 `outbox_event` 写入放在同一 MySQL 事务中；outbox-publisher 以 `FOR UPDATE SKIP LOCKED` 批量取未发布事件，幂等发布到 Kafka `rule_bcast`，成功后标记已发布。Debezium 只用于对账和审计，不作为 U0 唯一发布机制。
- **注记（实现现状）**：U0 单实例为按 id 顺序轮询发布（store.go 注释明示）；`FOR UPDATE SKIP LOCKED` 行级认领随 U2 多实例引入。Debezium 对账 U0/U1 关闭（docs/08 §3.0）。
- **理由**：避免“业务已提交但 Kafka 广播丢失”的双写不一致；Outbox 可在单库阶段落地，复杂度低于直接引入分布式事务。

### ADR-027 灾备与恢复目标

- **状态**：已采纳
- **决策**：U0 先完成同机与异机备份，U2 起执行跨可用区备份和恢复演练；MySQL 目标 RPO≤5 分钟/RTO≤30 分钟，Kafka RPO≤1 分钟/RTO≤15 分钟，Redis RPO≤5 分钟/RTO≤15 分钟，ClickHouse/Doris RPO≤15 分钟/RTO≤60 分钟。核心告警在线补拉依赖 72 小时保留窗口，不以 OLAP 备份替代在线投递状态。
- **理由**：高可用解决单点故障，灾备解决误删、机房级故障和不可逆损坏，两者必须分别验收。

### ADR-028 Flink 状态恢复与发布

- **状态**：已采纳
- **决策**：checkpoint 使用 S3/MinIO 外部存储，生产发布前创建 savepoint；状态启用 TTL，明确 watermark、迟到数据侧输出和状态清理策略；升级失败从最近 savepoint 回滚，禁止直接覆盖作业状态目录。
- **注记（实现现状）**：U0 为 checkpoint 60s + 默认本地状态后端（TickRuleJob）；S3/MinIO 目录（K8s 清单已配 s3://hq-flink-ckpt）与 savepoint 流程随 U2 生产化启用。
- **理由**：规则广播状态和窗口状态是实时链路的核心资产，必须能独立于 TaskManager 生命周期恢复和回滚。

### ADR-029 gateway_slot 租约 fencing

- **状态**：已采纳
- **决策**：Redis 槽位租约包含 TTL、续租、单调 `lease_version` 和 fencing token；生产者在消息中携带版本，ws-gateway 消费前校验当前版本；旧实例失去租约后进入 drain，拒绝继续消费和发送。
- **理由**：仅靠 TTL 不能避免网络分区后的旧实例继续消费，版本校验是防脑裂和重复推送的必要条件。

### ADR-030 Kafka Schema 与生产可靠性

- **状态**：已采纳
- **决策**：内部消息使用 Protobuf + Schema Registry 或等价兼容检查；禁止自动创建 Topic；生产者启用 `acks=all`、`enable.idempotence=true`、压缩和重试；Topic ACL 按服务最小权限配置，消息统一携带 `schema_version、trace_id、event_id`。
- **注记（实现现状）**：已落地 Protobuf 契约（proto 冻结）、禁自动建 Topic（kafka-init.sh 显式创建 + K8s auto.create.topics.enable=false）、trace_id/event_id 消息头；`acks=all`+幂等生产者随 U2 RF=3 启用（U0 单副本 RequireOne）；Schema Registry 与 ACL 随多租户/U2 评估；`schema_version` 头未启用（proto 冻结期内无演进需求）。
- **理由**：Kafka 可靠性不仅是副本数，还包括协议演进、权限、幂等和运维边界；这些约束必须在项目早期固定。

## B16~B20 落地变更补录（2026-09-07，补充先前未走变更控制的决策）

> docs/07 §6 变更控制要求冻结项变更必须新增 ADR；B16~B20（feature/llm-gateway）落地了
> 8 类关键变更但未即时登记，本节补录。编号接续 ADR-031。
> 注：ADR-031 在文件中位于 ADR-017 之后、018~030 之前（V2.1 冻结补充 ADR 节），属历史编号错位，
> 引用以编号为准（不做重排以免破坏既有交叉引用）。

### ADR-032 开发者接入迁移（V4 api_developer_config）

- **状态**：已实施（B16/B18，migrations/mysql/V4__api_developer.sql）
- **决策**：新增 `api_developer_config` 表承载第三方开发者接入（user_id/access_key/app_secret/push_mode/hook_url）；app_secret 自 B18 起用 `v1:` AES-GCM 密文存储（SecretBox，legacy 明文兼容）。
- **理由**：L3 开放层（docs/10 §6）的接入模型落地，为 AK/SK 鉴权与 WebHook 投递提供数据底座。

### ADR-033 JWT claim 契约三方对齐

- **状态**：已实施（B17，docs/11 §7.1）
- **决策**：统一 claim 契约 `sub`(uid 字符串, 保留) / `uid`(数值, ws-gateway 用) / `typ`(access|refresh, 三端都验) / `key`(APISIX consumer key)；ws-gateway 强制 `typ=access`（refresh 不得上 WS）；ws-bench 签发器同步。
- **理由**：修复 P0 契约断裂——原实现 uid 放 `sub`、ws-gateway 只认 `uid`、APISIX 要 `key` claim，真实用户链路端到端不通，仅压测自签 token 能走通。

### ADR-034 开放面 AK/SK 签名协议与 WS ticket

- **状态**：已实施（B18，docs/11 §5）
- **决策**：`/open/v1/*` 用 AK/SK 签名鉴权：`X-Signature=HMAC-SHA256(app_secret, METHOD\nPATH\nQUERY\nTS\nNONCE\nSHA256(body))` + 时间戳 ±300s + nonce 防重放（Redis SETNX 300s）+ per-AK 限流 + usage 计量；开发者 WS 走 60s 一次性 ticket（禁止 `?accessKey=` 直连，防 AK 进日志）。
- **理由**：取代 docs/10 原设计的裸 `Bearer accessKey` 与 `?accessKey=`，补防重放与密钥泄漏面；ticket 化避免 AK 进入 URL/访问日志。

### ADR-035 渠道凭据密文化（HQ_MASTER_KEYS / AES-GCM / KID）

- **状态**：已实施（B16，docs/11 §7.2）
- **决策**：平台主密钥 `HQ_MASTER_KEYS="k1=<hex64>[,k0=...]"`（env/K8s Secret 注入，仓库零字面量）；密文格式 `v1:<kid>:<base64url(nonce‖ct‖tag)>`，AES-256-GCM，AAD 绑定 kid，支持轮换并存；Go `secretx` / Java `SecretBox` / Python `secrets.py` 三端同格式；适用 `llm_provider.api_key_enc` / `notify_channel.config_enc` / `api_developer_config.app_secret`。
- **理由**：渠道凭据（LLM Key/邮件/短信 AK）需要入库热加载（管理台配置），明文落库不可接受；统一 KMS 简化实现（Dify/OneAPI 同款思路）。

### ADR-036 数据源演进（AKShare+sim → 东财实时 + Baostock 回填）

- **状态**：已实施（B3/B4，docs/09 §5.1）
- **决策**：真实行情切东方财富实时（source/eastmoney，频控退避/镜像轮换），历史 1m/5m 用 Baostock 回填；sim 源保留作压测/演示；MiniQMT 主力源因无券商账号暂缓。
- **理由**：docs/01/05 原定的 AKShare+自研模拟源无法提供真实实时行情，真实数据落地后才能支撑后续开发验证（docs/09:143 提前取用 M5 数据源项的理由）。

### ADR-037 LLM 多提供商网关

- **状态**：已实施（B16，扩展 ADR-010 的 ai-query）
- **决策**：`llm_provider/llm_model` 两表（提供商/模型，model_type=chat|embedding|image），平滑加权轮询 + 冷却熔断（连续失败 5 次→冷却 60s 起指数退避封顶 600s；401/403→300s；429→60s）+ 日配额（全局 2000/天 + 单 IP 50/天）+ TLS 预热 + asyncio 并发帽；admin 配置经 internal admin（X-Internal-Token）热生效（30s TTL）。
- **理由**：ai-query 单提供商硬编码 .env 是 Demo 级；多渠道轮询（OneAPI 模式）保证单一提供商故障时服务可用，且坏凭据自动冷却。

### ADR-038 通知渠道实装（SMS/IM/webhook 接线）

- **状态**：已实施（B16，扩展 ADR-006/ADR-019）
- **决策**：邮件适配器改为通用 SMTP（ssl/starttls，QQ/163/阿里/腾讯同配置面）；短信适配器实装阿里云 dysmsapi（腾讯/火山/百度 stub 预留）；钉钉/飞书 IM 与开发者 webhook 接线（dispatcher 补 webhook 分支 + api_developer_config + 出站 SSRF 校验 + IM 收件人修复）；渠道配置经 `notify_channel` 密文表 30s 热加载。
- **理由**：docs/10 标注"已实现"的渠道实际为纸面（main.go 传 nil、无调用点）；B16 补齐让三通道真实可用并可管理台配置。

### ADR-039 计量与审计数据表

- **状态**：已实施（B18/B19，V7/V8/V8b）
- **决策**：`open_usage_log`（按 access_key/path/status/latency 计量，计费数据底座）；`admin_audit_log`（管理操作留痕：grant 套餐/rotate secret 等）；`user.phone` 列（短信收件人）。
- **理由**：docs/12 §1.1 缺陷 #19/#20 登记——开放层无计量无法计费与成本控制，管理变更无审计不满足客诉与安全底线。

### ADR-040 token 吊销与 WS 套餐复核（已决策，2026-09-07）

- **状态**：已采纳
- **决策**：U1 接受 access token 2h 不可吊销（现状，无黑名单）；**WS 侧加 5min 套餐复核**——ws-gateway 每 5 分钟对在线连接按 user_id 复核 user_vip 配额与状态，降级/封号后存量连接在下一复核周期内被摘除或降权。复核查询走 Redis 缓存（user_vip 变更时由 biz-service 失效该用户缓存键），避免逐连接打库。
- **理由**：套餐降级/封号后存量 WS 连接持旧配额至重连是不可接受的（超配额推送 + 已封号用户仍收告警）；2h access 吊销需要黑名单/会话存储，U1 复杂度不值当，用 WS 侧定期复核兜底。
- **实现注记**：✅ **已实现（B22，71933d5）**——ws-gateway EntitlementReconciler 按 ENTITLE_RECHECK（默认 5min）复核在线连接权益：降级后下一复核周期内差频降档（free 10s）并摘除越权 kline 周期；查询走 Redis `user_vip:{id}`（biz-service VipCache 写路径同步，未命中直查 MySQL 回填）；端到端实证：连接在线时套餐降级，15s 复核周期内 gap 3s→10s。docs/11 §7.1 决策记录同步。

### ADR-041 掘金式行情订阅：扇出归属与订阅路由

- **状态**：已采纳（B21，docs/12 §2.4 分支 21）
- **决策**：
  1. **扇出归属 quote-push**：snapshot_kline 消费扇出并入 quote-push（新增 reader + Kline 路由），不新建独立进程——docs/04:89 契约表本就登记 quote-push 为 snapshot_kline 消费方（实现还账），且复用其 Router/writer/指标设施；
  2. **订阅索引不加 period 维度**：symgw 索引继续按 symbol 登记，kline 订阅亦走同一索引；channel/period 过滤下沉至 ws-gateway 连接内（Subs + KlinePeriods 二级判定）。代价：仅订 K 线的连接会引入少量无效 quote 扇出（网关内 Subs.Has + KlineSubs 拦截，不发客户端）——当前量级可接受，十万连接级再考虑独立索引；
  3. **兼容性承诺**：sub 消息 `channels` 字段为可选增强，缺省 = 纯 quote 行为，存量客户端零感知；周期枚举白名单制（quote@3s/kline@1m 起步，kline@3m~1d 枚举预留），未知 channel 忽略不炸连接；
  4. **亚分钟 bar 不落 CK**：3s/10s 仅走 ws_push 实时流（kline_local.period_min 保持分钟整型口径），落库扩展走独立 ADR。
- **理由**：掘金式订阅（symbol+period 订阅、闭合推送）与既有架构的预埋点（WsPushMsg.kline oneof、TypeKline/Channel 字段、snapshot_kline topic）完全对齐，最小实现路径即"接线预埋件"；把扇出归属既有服务、索引维度不变，避免为单一功能引入新进程与新索引结构。
