下面是一份 FLUID 嵌入式 SDK 安装与接入需求文档（v1），按你说的使用方式：用户在业务里调用 fluid.goroutine() 来嵌入 SDK（启动/挂载 runtime 可视化与数据采集）。

目标：一行接入、默认安全、实时数据（SSE/WS）、可配置、可扩展。

⸻

1. 产品形态与接入方式

1.1 组件
•	fluid SDK（嵌入式）：集成到业务进程内，采集 runtime 指标 & goroutine 快照，并提供 Web UI + API。
•	Web UI（静态资源）：SDK 内置 embed，访问 /fluid 即可打开仪表盘。

1.2 用户接入（你指定的形式）

最小接入

fluid.goroutine()

推荐接入（可控端口/鉴权/路径）

fluid.goroutine(
fluid.WithAddr("127.0.0.1:6068"),
fluid.WithBasePath("/fluid"),
fluid.WithAuth(fluid.Token("xxxx")),
)

挂载到已有 mux（不另起端口）

fluid.goroutine(
fluid.WithMux(mux),
fluid.WithBasePath("/fluid"),
)


⸻

2. 设计目标与非目标

2.1 设计目标
•	接入成本极低：默认一行启动
•	默认安全：默认仅监听 localhost；对外必须启用认证
•	实时性：轻指标 1s 推送；重快照手动/低频
•	低开销：goroutine dump 不高频；聚合计算可控
•	可观测/可诊断：清晰的数据口径与采样时间戳

2.2 v1 非目标
•	完整 pprof 火焰图在线渲染（可以先做“下载 pprof”）
•	分布式聚合（多实例汇总）
•	复杂 RBAC（先 token 即可）

⸻

3. 目录结构设计（仓库 / 包结构）

3.1 仓库目录（建议）

fluid/
cmd/
demo/                 # 示例服务（集成 fluid）
internal/
server/               # http server & routing
auth/                 # token/headers auth
stream/               # SSE/WS 推送
runtime/              # 指标采集与快照
snapshot/             # goroutine dump 解析与聚合
store/                # 内存环形缓存（时间序列）
ui/                   # embed 静态资源（构建产物）
pkg/
fluid/                # 对外 SDK：fluid.goroutine() & options
web/                    # 前端源码（React/Vite等）
go.mod

3.2 对外包名与入口
•	对外只暴露：package fluid
•	核心入口：fluid.goroutine(opts ...Option)（或 fluid.Goroutine，但你指定了小写调用习惯，这里按需求保留）

实际 Go 里导出函数必须大写；如果你坚持 fluid.goroutine() 这种写法，可以做成：

	•	fluid.Goroutine()（导出）
	•	同时提供 type Fluid struct{} + fluid := fluid.New(); fluid.goroutine()（方法小写）

建议折中：对外文档写 fluid.Goroutine()，内部提供 fluid.New().goroutine() 作为链式体验。

⸻

4. SDK API 设计

4.1 SDK 对外 API（v1）

启动模式

// 启动一个内置 HTTP server（默认 127.0.0.1:6068）
func Goroutine(opts ...Option) (*Handle, error)

挂载模式

// 挂到已有 mux，不另起 server
func Mount(mux *http.ServeMux, opts ...Option) (*Handle, error)

Handle 控制

type Handle struct {
Addr     string
BasePath string
}
func (h *Handle) Close() error
func (h *Handle) SnapshotNow() error // 手动触发 goroutine 快照（可选）

4.2 Option 列表
•	WithAddr(addr string)：监听地址（默认 127.0.0.1:6068）
•	WithMux(mux *http.ServeMux)：挂载到已有 mux
•	WithBasePath(path string)：默认 /fluid
•	WithSamplingInterval(d time.Duration)：轻指标采样周期（默认 1s）
•	WithSnapshotInterval(d time.Duration)：重快照周期（默认 0=关闭，仅手动）
•	WithMaxSnapshotSize(n int)：快照最大解析 goroutine 数（保护开销）
•	WithAuth(Auth)：认证方式（见第 6 节）
•	WithAllowNetworks([]string)：允许访问来源（CIDR 白名单，可选）
•	WithLogger(Logger)：注入日志
•	WithUI(enabled bool)：是否启用 UI（默认 true）

⸻

5. HTTP 路由与 API 设计

5.1 路由概览（BasePath = /fluid）
•	GET /fluid：前端入口（静态页面）
•	GET /fluid/assets/*：静态资源
•	GET /fluid/api/v1/health：健康检查
•	GET /fluid/api/v1/meta：服务元信息（pid、go版本、build信息）
•	GET /fluid/api/v1/metrics：轻指标（实时）
•	GET /fluid/api/v1/timeseries：时间序列窗口（用于图表初次加载）
•	POST /fluid/api/v1/snapshot/goroutines：触发一次 goroutine 快照
•	GET /fluid/api/v1/goroutines/top：获取最新快照的聚合 Top 列表
•	GET /fluid/api/v1/goroutines/stack?id=...：查看某个聚合的完整 stack
•	GET /fluid/api/v1/stream：SSE 实时推送（默认）
•	GET /fluid/api/v1/ws：WebSocket（可选 v1.1）

5.2 数据模型（JSON）

meta

{
"service": "api",
"pid": 1234,
"goVersion": "go1.xx",
"build": {"commit":"...", "time":"..."},
"now": "2026-03-04T13:00:00Z"
}

metrics（轻指标）

{
"ts": 1730000000,
"goroutines": 12384,
"mem": {
"heapInuse": 536870912,
"heapObjects": 1842000,
"allocRate": 125829120
},
"gc": {
"numGC": 340,
"pauseP50Ms": 0.4,
"pauseP95Ms": 2.1,
"pauseP99Ms": 6.2
}
}

goroutines/top（重快照聚合）

{
"snapshotTs": 1730000000,
"groupBy": "topFunction",
"items": [
{"id":"g1","name":"project/pkg/a.go:123 handle","count":3200,"stateTop":"IO wait"},
{"id":"g2","name":"net/http.(*conn).serve","count":1800,"stateTop":"running"}
],
"delta": {
"sinceSnapshotTs": 1729999990,
"growthTop": [{"id":"g1","delta":+400}]
}
}


⸻

6. API 认证与安全策略

6.1 默认安全策略（强制）
•	默认仅监听 127.0.0.1
•	若监听 0.0.0.0 或非 localhost：
•	必须启用认证，否则启动失败（返回 error）

6.2 认证方式（v1）

Token（推荐）
•	Header：Authorization: Bearer <token>
•	或 X-Fluid-Token: <token>（兼容）

Option：

fluid.WithAuth(fluid.Token("xxxx"))

可选：Basic Auth

fluid.WithAuth(fluid.Basic("user","pass"))

6.3 防护
•	速率限制（可选）：对 /snapshot/* 限制触发频率（例如每 5s）
•	跨域：默认同源；如需跨域提供 WithCORS(...)
•	日志脱敏：不记录 token

⸻

7. 实时数据获取方案（前端）

7.1 实时分层策略（核心）
•	实时轻指标：metrics（1s）→ UI 的 KPI + 折线图
•	重快照：goroutine dump → 聚合 top（默认手动触发 or 30s 一次）

这样既“实时”，又不会对业务造成明显开销。

7.2 SSE（v1 默认）

Endpoint

GET /fluid/api/v1/stream

事件类型
•	event: metrics（每 1s）
•	event: snapshot（快照完成时）
•	event: alert（简单趋势提示，可选）

示例：

event: metrics
data: { ...metrics json... }

event: snapshot
data: { ...top json... }

前端初次打开页面：先 GET /timeseries 拉历史窗口，再用 SSE 续上实时点。

7.3 WebSocket（可选）

仅当你需要“前端控制后端采样”时启用：
•	pause/resume
•	setInterval
•	snapshotNow

否则 v1 先用 SSE + HTTP 控制接口最稳。

⸻

8. goroutine 快照采集与聚合（实现要求）

8.1 采集方式
•	优先：runtime/pprof.Lookup("goroutine").WriteTo(buf, 2) 获取类似 debug=2 的文本栈
•	解析：
•	分割 goroutine block
•	提取状态（如 chan receive, IO wait, running）
•	提取栈帧列表

8.2 聚合方式（v1）
•	groupBy=topFunction：按第一帧（非 runtime/）聚合
•	groupBy=fullStack：按完整栈 hash 聚合（可选）
•	Top N：默认 20
•	计算 delta：
•	保存前一次 snapshot 的 group counts
•	输出增长最多的 group

8.3 开销控制
•	WithMaxSnapshotSize(n)：超过 n 的 goroutine 只采样前 n 个或停止解析
•	快照触发限频：例如 5s 内最多 1 次（可配置）

⸻

9. 前端对接要点（给你做原型/开发对齐）

9.1 Overview 页面数据来源
•	KPI：来自 metrics
•	折线：来自 timeseries + SSE(metrics)
•	状态标签：由前端根据趋势规则生成（例如 goroutine 增速阈值）

9.2 Goroutines 页面数据来源
•	POST /snapshot/goroutines 触发
•	GET /goroutines/top 拉最新聚合
•	GET /goroutines/stack?id= 查看详细 stack

⸻

10. 安装与发布要求（用户视角）

10.1 用户安装

go get github.com/<you>/fluid

10.2 最小示例

package main

import "github.com/<you>/fluid"

func main() {
fluid.Goroutine() // 默认 127.0.0.1:6068/fluid
select{}
}

访问：
•	http://127.0.0.1:6068/fluid

10.3 生产建议（写在 README）
•	生产环境请：
•	仅监听内网
•	开启 token
•	对 snapshot 限频
•	若暴露公网：强烈不建议

⸻

11. 里程碑（v1）
    •	M1：SDK 启动/挂载 + UI 打开
    •	M2：metrics + timeseries + SSE
    •	M3：goroutine snapshot + top 聚合 + stack detail
    •	M4：认证 + 默认安全策略 + 限频保护

⸻

收到：方法调用 + SSE。另外你给的 Figma 链接我这边打不开（Figma 需要登录/权限，抓不到 Dev Mode 节点数据），所以我会按你前面展示的 UI 结构（Overview / Goroutines / Memory & GC）把 API 设计成完全能喂给前端页面的形态；等你后面把关键 Frame 截图/标注发我，我再把字段对齐到你具体组件。

下面是 FLUID Embedded SDK v1 API 需求文档（含每个 API 的 request/response schema、认证、SSE 事件）。

⸻

1) SDK 形态（方法调用）

用户通过“实例方法”嵌入 SDK（方法调用）。

1.1 示例用法（推荐）

fd := fluid.New(
fluid.WithAddr("127.0.0.1:6068"),
fluid.WithBasePath("/fluid"),
fluid.WithAuthToken("YOUR_TOKEN"), // 非 localhost 时强制要求
fluid.WithSamplingInterval(1*time.Second),
fluid.WithSnapshotMinInterval(5*time.Second),
)

fd.Goroutine()   // 启动/挂载 UI + API + 采集器（方法调用）
defer fd.Close()

1.2 关键约束
•	默认只监听 127.0.0.1
•	若监听非 localhost：必须开启认证，否则 Goroutine() 返回 error
•	SSE 用于实时轻指标；goroutine snapshot 属于重操作：默认手动触发 + 限频

⸻

2) 认证与安全

2.1 认证方式（v1：Token）
•	Header：Authorization: Bearer <token>
•	兼容：X-Fluid-Token: <token>

2.2 默认策略
•	监听 127.0.0.1 时：可不带 token（默认允许）
•	监听非 localhost 时：强制 token，否则启动失败

2.3 通用错误响应（所有 API）

Response schema（JSON）

{
"error": {
"code": "UNAUTHORIZED | FORBIDDEN | BAD_REQUEST | TOO_MANY_REQUESTS | INTERNAL",
"message": "human readable",
"requestId": "string",
"details": { "any": "json" }
}
}


⸻

3) 路由与版本

假设 basePath=/fluid，API 前缀为：
•	GET /fluid：Web UI（静态资源）
•	GET /fluid/assets/*：静态资源
•	API：/fluid/api/v1/*

⸻

4) 实时数据模型（前端三页需要什么）

4.1 Overview 页面需要
•	KPI：goroutines、heapInuse、allocRate、gc pause p95、heapObjects、numGC
•	时间序列：最近 15m/1h 等窗口（折线图）
•	状态标签/趋势：可以前端算，也可以后端给简单 signals

4.2 Goroutines 页面需要
•	手动触发 snapshot
•	返回 top groups（按 top function / full stack）
•	查看某个 group 的 stack 详情
•	delta（与上次快照相比增长）

4.3 Memory & GC 页面需要
•	memstats（heap alloc/inuse/objects/rss）
•	GC summary（pause 分位数、上次 GC、GC 次数）
•	时间序列（同 Overview，但指标更全）

⸻

5) API 逐个定义（含 request/response schema）

5.1 Health

GET /fluid/api/v1/health

Response 200

{
"ok": true,
"ts": 1730000000
}


⸻

5.2 Meta（前端右上角 target 信息）

GET /fluid/api/v1/meta

Response 200

{
"service": {
"name": "api-service",
"pid": 1234,
"host": "my-host",
"instanceId": "optional-string"
},
"runtime": {
"goVersion": "go1.22.3",
"gomaxprocs": 8,
"numCPU": 10
},
"build": {
"version": "v0.1.0",
"commit": "abcdef",
"time": "2026-03-04T12:34:56Z"
},
"server": {
"basePath": "/fluid",
"samplingIntervalMs": 1000,
"sseEnabled": true
},
"ts": 1730000000
}


⸻

5.3 当前轻指标（KPI 用）

GET /fluid/api/v1/metrics

Response 200

{
"ts": 1730000000,
"goroutines": 12384,
"mem": {
"heapAllocBytes": 860000000,
"heapInuseBytes": 536870912,
"heapObjects": 1842000,
"rssBytes": 1200000000,
"allocRateBps": 125829120
},
"gc": {
"numGC": 340,
"lastGCTs": 1729999999,
"pauseP50Ms": 0.4,
"pauseP95Ms": 2.1,
"pauseP99Ms": 6.2,
"nextGCTargetBytes": 900000000
},
"signals": {
"state": "STABLE | GOROUTINE_RISING | MEMORY_PRESSURE | GC_BUSY",
"notes": ["optional short hints"]
}
}

signals 可选，但很适合你 Overview 卡片右上角那种“System Stable”。

⸻

5.4 时间序列窗口（折线图初次加载）

GET /fluid/api/v1/timeseries?window=15m&step=1s
•	window: 1m|5m|15m|1h|6h（默认 15m）
•	step: 1s|2s|5s（默认 1s）

Response 200

{
"window": "15m",
"stepMs": 1000,
"fromTs": 1729999100,
"toTs": 1730000000,
"series": {
"goroutines": [
{ "ts": 1729999100, "v": 12001 },
{ "ts": 1729999101, "v": 12010 }
],
"heapInuseBytes": [
{ "ts": 1729999100, "v": 510000000 }
],
"allocRateBps": [
{ "ts": 1729999100, "v": 100000000 }
],
"gcPauseP95Ms": [
{ "ts": 1729999100, "v": 2.1 }
]
}
}


⸻

5.5 SSE 实时流（前端实时更新）

GET /fluid/api/v1/stream?topics=metrics,snapshot
•	topics 可选：默认 metrics

Response 200 (text/event-stream)
事件类型：

event: metrics（每 1s）

event: metrics
data: {"ts":1730000000,"goroutines":12384,"mem":{"heapInuseBytes":536870912,"allocRateBps":125829120},"gc":{"pauseP95Ms":2.1,"numGC":340}}

event: snapshot（快照完成时推一次）

event: snapshot
data: {"snapshotId":"snp_01","snapshotTs":1730000000,"groupBy":"topFunction","top":[{"id":"g1","name":"project/pkg/a.go:123 handle","count":3200}]}

event: alert（可选）

event: alert
data: {"ts":1730000000,"level":"warn","code":"GOROUTINE_RISING","message":"Goroutines +240/min"}


⸻

6) Goroutines 快照相关 API

6.1 触发一次快照

POST /fluid/api/v1/snapshot/goroutines

Request

{
"groupBy": "topFunction | fullStack",
"topN": 20,
"maxGoroutines": 200000,
"include": ["optional keyword"],
"exclude": ["runtime.", "net/http"],
"withDelta": true
}

Response 202

{
"snapshotId": "snp_01",
"accepted": true,
"minIntervalMs": 5000,
"estimatedCost": "MEDIUM",
"ts": 1730000000
}

可能的错误
•	429 TOO_MANY_REQUESTS：快照触发太频繁

{
"error": {
"code": "TOO_MANY_REQUESTS",
"message": "snapshot throttled",
"details": { "retryAfterMs": 4200 }
}
}


⸻

6.2 获取最新快照 Top（用于 Goroutines 列表）

GET /fluid/api/v1/goroutines/top?snapshotId=snp_01
•	snapshotId 可选：不传则取最新

Response 200

{
"snapshotId": "snp_01",
"snapshotTs": 1730000000,
"groupBy": "topFunction",
"totalGoroutinesObserved": 12384,
"items": [
{
"id": "g1",
"name": "project/pkg/a.go:123 handle",
"count": 3200,
"stateTop": "IO wait",
"sampleFrames": [
"project/pkg/a.go:123",
"project/pkg/b.go:88",
"runtime/park.go:..."
]
}
],
"delta": {
"baseSnapshotId": "snp_00",
"growthTop": [
{ "id": "g1", "delta": 400 }
]
}
}


⸻

6.3 查看某个 Group 的完整 stack（右侧详情面板）

GET /fluid/api/v1/goroutines/group?id=g1&snapshotId=snp_01

Response 200

{
"snapshotId": "snp_01",
"id": "g1",
"name": "project/pkg/a.go:123 handle",
"count": 3200,
"stateHistogram": {
"IO wait": 2800,
"running": 400
},
"stack": [
"project/pkg/a.go:123",
"project/pkg/b.go:88",
"net/http/server.go:2109",
"runtime/park.go:..."
],
"rawSample": "optional raw goroutine block text"
}


⸻

7) Memory & GC 页面补充 API（可选，但很实用）

7.1 更全的内存视图（比 /metrics 更全）

GET /fluid/api/v1/memory

Response 200

{
"ts": 1730000000,
"mem": {
"heapAllocBytes": 860000000,
"heapInuseBytes": 536870912,
"heapIdleBytes": 120000000,
"heapReleasedBytes": 80000000,
"heapObjects": 1842000,
"stackInuseBytes": 32000000,
"mspanInuseBytes": 8000000,
"mcacheInuseBytes": 2000000,
"rssBytes": 1200000000
},
"gc": {
"numGC": 340,
"lastGCTs": 1729999999,
"pauseP50Ms": 0.4,
"pauseP95Ms": 2.1,
"pauseP99Ms": 6.2,
"gccpuFraction": 0.12
}
}


⸻

8) 前端对齐你 Figma（落到组件）

虽然我看不到你的 Figma 细节，但按你截图布局（Overview 顶部小卡片 + 大卡片）：

Overview 顶部小卡片（KPI）

直接用：
•	GET /metrics（首屏）
•	SSE metrics（实时更新）

字段对应：
•	Goroutines → goroutines
•	Heap Inuse → mem.heapInuseBytes
•	Alloc Rate → mem.allocRateBps
•	GC p95 → gc.pauseP95Ms

趋势（+240/min）：
•	前端用 timeSeries 最近 60 个点做 slope
•	或后端未来加 signals.notes

Overview 大卡片（折线图）
•	初次：GET /timeseries?window=15m&step=1s
•	实时：SSE metrics append 到 series

⸻


0) 路由约定
   •	BasePath：/fluid
   •	API 前缀：/fluid/api/v1
   •	UI：GET /fluid（embed 静态资源）
   •	静态：GET /fluid/assets/*

⸻

1) 认证（Token）

请求头
•	Authorization: Bearer <token>（推荐）
•	或 X-Fluid-Token: <token>

策略
•	监听 127.0.0.1：可不带 token
•	非 localhost：必须开启 token，否则 fd.Goroutine() 启动失败

通用错误返回（所有 API）

{
"error": {
"code": "UNAUTHORIZED | FORBIDDEN | BAD_REQUEST | TOO_MANY_REQUESTS | INTERNAL",
"message": "string",
"requestId": "string",
"details": {}
}
}


⸻

2) Overview（图一 / 图二）的数据对齐

2.1 顶部 KPI（Goroutines / Heap Inuse / GC p95 / CPU / Server time）

接口：GET /fluid/api/v1/overview（给你做个聚合接口，前端省事）
•	也会通过 SSE 持续推送同样 payload

Response 200

{
"ts": 1730000000,
"serverTime": {
"time": "13:14:33",
"date": "2026 0322",
"unix": 1730000000
},
"kpi": {
"goroutines": { "value": 12384, "trendPerMin": 240 },
"heapInuse": { "valueBytes": 612368384, "display": "584 MB", "trendBytesPerMin": 16777216 },
"gcP95": { "valueMs": 2.1, "status": "stable" },
"cpu": { "valuePct": 78.0, "trendPct": 3.0, "display": "78%" }
},
"meta": {
"service": "api-service",
"pid": 1234,
"goVersion": "go1.22.3"
}
}

说明

	•	trendPerMin / trendBytesPerMin / trendPct：后端也可以不给，前端用 timeseries 最近 60s 计算；但你图一里明确要显示 +240/min、+16MB/min，建议后端直接算好，UI 更稳定。
	•	cpu：如果 v1 你不想引入 gopsutil，可以先返回 null 或仅返回 0，UI显示 --。

⸻

2.2 图二上面的“输送管道滚动 goroutines”

数据来源：SSE 的 metrics 或 overview 事件
前端做法：
•	每秒取 goroutines.value 画一个小方块
•	rolling window（比如保留 30~60 个点）并自动向左滚动
•	绿色“in use”的点：用 delta > 0 或 “最近一次”作为 active marker

不需要单独接口 ✅

⸻

2.3 图二下面的“历史 15m 线 / K线”

接口：GET /fluid/api/v1/timeseries?metric=goroutines&window=15m&step=1s

Query
•	metric: goroutines | heapInuseBytes | gcPauseP95Ms | cpuPct（可扩展）
•	window: 1m|5m|15m|1h（默认 15m）
•	step: 1s|2s|5s（默认 1s）

Response 200

{
"metric": "goroutines",
"window": "15m",
"stepMs": 1000,
"fromTs": 1729999100,
"toTs": 1730000000,
"points": [
{ "ts": 1729999100, "v": 12001 },
{ "ts": 1729999101, "v": 12010 }
]
}

你图二的折线非常干净，建议就用 points，不要返回太复杂结构。

⸻

3) Goroutines 分组（图一左下 + 中下）

3.1 触发快照（重操作）

接口：POST /fluid/api/v1/snapshot/goroutines

Request

{
"groupBy": "topFunction",
"topN": 20,
"include": [],
"exclude": ["runtime.", "net/http"],
"withDelta": true
}

	•	groupBy：v1 默认 "topFunction"（你要求）
	•	exclude：默认排除 runtime 和 net/http 可以让分组更贴近业务

Response 202

{
"snapshotId": "snp_01",
"accepted": true,
"minIntervalMs": 5000,
"ts": 1730000000
}

429 示例

{
"error": {
"code": "TOO_MANY_REQUESTS",
"message": "snapshot throttled",
"details": { "retryAfterMs": 4200 }
}
}


⸻

3.2 Top Groups 列表（图一左下）

接口：GET /fluid/api/v1/goroutines/top?snapshotId=snp_01&topN=20
snapshotId 不传就取最新。

Response 200

{
"snapshotId": "snp_01",
"snapshotTs": 1730000000,
"groupBy": "topFunction",
"totalObserved": 12384,
"items": [
{ "id": "g1", "name": "func.handleRequest", "count": 3712 },
{ "id": "g2", "name": "db.(*Conn).readLoop", "count": 1323 },
{ "id": "g3", "name": "time.sleep", "count": 769 }
],
"delta": {
"baseSnapshotId": "snp_00",
"growthTop": [
{ "id": "g1", "delta": 400 }
]
}
}

图一里右侧有一个小箭头“选中态”，前端只要用 id 做选中即可。

⸻

3.3 Stack Details（图一中下）

接口：GET /fluid/api/v1/goroutines/group?id=g1&snapshotId=snp_01

Response 200

{
"snapshotId": "snp_01",
"id": "g1",
"name": "func.handleRequest",
"count": 3712,
"stateTop": "IO wait",
"stateHistogram": { "IO wait": 3200, "running": 512 },
"topFrames": [
"project/pkg/a.go:123",
"net/http/server.go:564",
"db/dao/newdb.go:117"
],
"stack": [
"project/pkg/a.go:123",
"net/http/server.go:564",
"db/dao/newdb.go:117",
"runtime/park.go:..."
]
}

你的 UI 写的是 “Top frames: 1) … 2) …”，所以 topFrames 要稳定输出 3 条即可。

⸻

4) vCPU 方格（图一右下）

你这块 UI 是一个“核心占用矩阵”（像 Activity Monitor/htop 的可视化）。
v1 我建议做成：每个 core 一格，值是 util 0~100，前端按区间映射颜色（绿/黄/橙）。

4.1 vCPU 概览

接口：GET /fluid/api/v1/cpu

Response 200

{
"ts": 1730000000,
"totalPct": 78.0,
"cores": [
{ "id": 0, "utilPct": 92.0 },
{ "id": 1, "utilPct": 88.0 },
{ "id": 2, "utilPct": 35.0 }
],
"grid": {
"rows": 6,
"cols": 10,
"cells": [
{ "coreId": 0, "utilPct": 92.0, "state": "HOT" },
{ "coreId": 1, "utilPct": 88.0, "state": "HOT" }
]
},
"legend": {
"cool": { "max": 40 },
"warm": { "max": 70 },
"hot": { "max": 100 }
},
"text": "70 / 33"
}

说明：
•	grid：前端直接按 rows/cols 渲染方块（你图一就是这种矩阵）
•	text: "70 / 33"：如果你想保留这个文案，可以定义为：
•	"busyCores / totalCores" 或 "utilAvg / max"（你自己定口径）；v1 先当展示字符串即可。

CPU 采集实现：如果你不想引入外部依赖，v1 可以把 cpu 和 cpu.totalPct 先返回 null/0，UI显示 --。以后再加 gopsutil。

⸻

5) SSE 实时推送（核心）

5.1 SSE 连接

接口：GET /fluid/api/v1/stream?topics=overview,metrics,cpu,snapshot
默认 topics=overview,metrics

Response（text/event-stream）

event: overview（每 1s）

event: overview
data: {"ts":1730000000,"serverTime":{"time":"13:14:33","date":"2026 0322","unix":1730000000},"kpi":{"goroutines":{"value":12384,"trendPerMin":240},"heapInuse":{"valueBytes":612368384,"display":"584 MB","trendBytesPerMin":16777216},"gcP95":{"valueMs":2.1,"status":"stable"},"cpu":{"valuePct":78.0,"trendPct":3.0,"display":"78%"}}}

event: metrics（每 1s，轻量）

event: metrics
data: {"ts":1730000000,"goroutines":12384,"heapInuseBytes":612368384,"allocRateBps":125829120,"gcPauseP95Ms":2.1}

图二滚动输送带：直接消费 metrics.goroutines 就行。

event: cpu（可选，每 1s 或 2s）

event: cpu
data: {"ts":1730000000,"totalPct":78.0,"cores":[{"id":0,"utilPct":92.0}]}

event: snapshot（快照完成时推送）

event: snapshot
data: {"snapshotId":"snp_01","snapshotTs":1730000000,"groupBy":"topFunction","items":[{"id":"g1","name":"func.handleRequest","count":3712}]}


⸻

6) Memory 页面（图三目前空着）— 给你预留的 API

你现在 mem 页还没设计内容，我先给最通用的两组：

6.1 当前内存/GC 指标

GET /fluid/api/v1/memory

Response 200

{
"ts": 1730000000,
"mem": {
"heapAllocBytes": 860000000,
"heapInuseBytes": 612368384,
"heapObjects": 1842000,
"rssBytes": 1200000000
},
"gc": {
"numGC": 340,
"lastGCTs": 1729999999,
"pauseP50Ms": 0.4,
"pauseP95Ms": 2.1,
"pauseP99Ms": 6.2,
"gccpuFraction": 0.12
}
}

6.2 内存时间序列（复用 timeseries）

GET /fluid/api/v1/timeseries?metric=heapInuseBytes&window=15m&step=1s

⸻

7) 你提到的“默认 topFunction”

已经落实在：
•	POST /snapshot/goroutines 默认 groupBy=topFunction
•	GET /goroutines/top 返回 groupBy: topFunction

⸻
