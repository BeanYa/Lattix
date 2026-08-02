# IP 质量测试脚本化重构设计

日期：2026-08-02
状态：已确认

## 背景与目标

现有 IP 质量测试（`ServerTestIPQuality`）为项目内 Go 原生实现（见 `docs/server-testing-design.md` 中"不下载或执行上游脚本"的条款），因权限/网络等原因数据不全，且维护成本高。本次重构：

1. 删除 agent 侧全部 Go 原生 IP 质量实现。
2. 改为拉取并执行上游脚本 [xykt/IPQuality](https://github.com/xykt/IPQuality)（`ip.sh`），采用其参数运行模式。
3. 以 `-p`（隐私模式）禁止在线报告生成/上传，`-j` 输出 JSON（stdout）。
4. 完成后解析其 JSON 结果，映射为 Lattix 自有强类型数据格式，经现有 `server-test.result` 协议上报 panel。
5. **新报告结构必须包含脚本 JSON 输出的全部字段**，不沿用现有 Go 实现的报告结构（其数据不全）。
6. Panel 前端报告表按新结构重写，展示完整报告。

## 现有代码删除清单

| 文件 | 说明 |
|---|---|
| `src/agent/internal/servertest/ip_quality.go` | runIPQuality / runIPFamilyQuality 主实现 |
| `src/agent/internal/servertest/ip_providers.go` | 10 家 IP 数据库聚合实现 |
| `src/agent/internal/servertest/dnsbl.go` | DNSBL 查询实现 |
| `src/agent/internal/servertest/dnsbl_snapshot.txt` | 嵌入式 DNSBL 域名快照 |
| `src/agent/internal/servertest/asn.go` | Team Cymru ASN 查询 |
| `src/agent/internal/servertest/ip_providers_test.go` | 对应测试 |
| `src/agent/internal/servertest/dnsbl_test.go` | 对应测试 |
| `src/agent/internal/servertest/asn_test.go` | 对应测试 |
| `src/agent/internal/servertest/runner.go` | 移除 `{name: "ip_quality", run: r.runIPQuality}` 注册及引用 |

保留：worker 隔离、manager 任务/分片上报、panel 存储、协议外层结构（`ServerTestRunPayload` / `ServerTestProgressPayload` / `ServerTestResultManifest` / `ServerTestReport`）。

## 架构与数据流

```
panel ── server-test.run ──▶ agent Manager ──▶ 隔离 worker 进程
worker:
  prepareScript(): 缓存 <dataDir>/scripts/ip.sh，拉取最新，比对 script_version，必要时原子替换
  ensureDeps():    LookPath 检测 jq/curl/bc/nc/dig/ip；缺失 → 执行 bash ip.sh -y -p 安装轮次
  runScript():     bash ip.sh -p -j -f（双栈），stdout 捕获，15 分钟超时
  parse():         json.Decoder 流式拆解 v4/v6 两个 JSON 文档 → 规范化 → 映射 IPQualityResult
  ──▶ 汇总进 ServerTestReport ──▶ 分片上报 panel ──▶ store ──▶ 前端报告表
```

执行环境仍在现有隔离 worker（`worker.go` 自我 fork + namespace/rlimit）内，网络 namespace 共享（脚本需联网查询）。

## 脚本管理：缓存 + 版本校验

- 缓存路径：`<dataDir>/scripts/ip.sh`（`dataDir` 即 manager 现有数据目录）。
- 每次运行：经 `lattix/shared/requester`（`ExternalFileRequester`）下载最新 `ip.sh`（约 108KB，raw.githubusercontent.com），解析其中 `script_version`：
  - 与缓存版本相同 → 直接用缓存；
  - 不同 → 写临时文件后原子替换缓存（沿用 selfupdate 的校验-替换模式）；
  - 下载失败 → 回退缓存，报告标记 `script_stale: true`。
- 脚本运行时自拉 `ref/` 资产（iso3166.json 等，raw.githubusercontent + jsdelivr 回退），脚本自身处理，不需干预。

## 依赖处理

- Go 侧 `exec.LookPath` 检测：`jq`、`curl`、`bc`、`nc`、`dig`、`ip`（iproute2）。
- 全部存在 → 以 `-n` 跳过脚本自身检测，直接运行。
- 有缺失 → 先执行安装轮次：`bash ip.sh -y -p -4`，Go 侧设短超时（120 秒，覆盖 apt/dnf 等装包时间；脚本装完依赖会继续跑 v4 检测，超时截断）。结束后 Go 侧复查 `LookPath`：
  - 就绪 → 正式执行（带 `-n`）；
  - 仍缺 → 测试失败，错误信息列出缺失命令。
- 安装轮次在 worker 内执行（继承 root 权限）。

## 执行细节

- 命令：`bash <cached ip.sh> -p -j -f`（双栈默认）。
  - `-p`：隐私模式，禁止在线报告生成（不上传 upload.check.place）；
  - `-j`：stdout 输出 JSON；
  - `-f`：展示完整 IP（用户要求）；
  - 不指定网卡/代理（直连测服务器真实出口）。
- 超时：单次执行上限 15 分钟（脚本含媒体/邮件/DNSBL 全量检测，典型 3–8 分钟），沿用 60 分钟任务窗口；超时 → kill 进程组，状态 failed。
- 进度：脚本无机器可读进度，沿用"进度为尽力上报"机制，阶段文案：下载脚本 → 检查依赖 → 运行检测 → 解析结果。
- 退出码 → 错误映射表（1 参数错误、10 输出文件已存在、11 不可写、其余 → 一般失败；`-4`/`-6` 专属的 4/6 错误码本设计不使用，无需处理）。

## Lattix 数据格式（`src/shared/server_testing.go`）

`ServerTestCategoryResult` 新增强类型字段（其它分类不受影响）：

```go
type ServerTestCategoryResult struct {
    Category     ServerTestCategory `json:"category"`
    Status       string             `json:"status"`
    Summary      map[string]any     `json:"summary,omitempty"`
    Items        []map[string]any   `json:"items,omitempty"`
    IPQuality    *IPQualityResult   `json:"ip_quality,omitempty"`   // 新增
    ErrorCode    string             `json:"error_code,omitempty"`
    ErrorMessage string             `json:"error_message,omitempty"`
}
```

`IPQualityResult` 覆盖脚本 JSON 全部字段（Head/Info/Type/Score/Factor/Media/Mail，双栈两家族）：

```go
type IPQualityResult struct {
    SchemaVersion int                    `json:"schema_version"`
    ScriptVersion string                 `json:"script_version"`
    ScriptStale   bool                   `json:"script_stale,omitempty"`
    Families      []IPQualityFamily      `json:"families"`
}

type IPQualityFamily struct {
    Family ServerTestAddressFamily `json:"family"`   // ipv4 | ipv6（按 Head.IP 格式判定）
    Head   IPQualityHead           `json:"head"`     // IP(完整), Command, GitHub, Time, Version
    Info   IPQualityInfo           `json:"info"`     // ASN, Organization, Latitude, Longitude, DMS, Map, TimeZone,
                                                     // City{Name,PostalCode,SubCode,Subdivisions},
                                                     // Region{Code,Name}, Continent{Code,Name},
                                                     // RegisteredRegion{Code,Name}, Type
    Type   IPQualityType           `json:"type"`     // Usage map[db]string, Company map[db]string
    Score  IPQualityScore          `json:"score"`    // map[db]string（保留脚本原始字符串，含百分数/"null"）
    Factor IPQualityFactor         `json:"factor"`   // CountryCode/Proxy/Tor/VPN/Server/Abuser/Robot
                                                     // 每因子 map[db]*bool（null → nil）
    Media  map[string]IPQualityMediaStatus `json:"media"` // 按服务名 key（TikTok/DisneyPlus/Netflix/Youtube/
                                                          // AmazonPrimeVideo/Reddit/ChatGPT，服务增删不破坏协议）
    Mail   IPQualityMail           `json:"mail"`     // Port25 *bool, 每服务 *bool, DNSBlacklist{Total,Clean,Marked,Blacklisted}
    Raw    json.RawMessage         `json:"raw,omitempty"` // 规范化后的原始 JSON 副本（排障/无损）
}
```

规范化规则：
- 脚本的 `"null"` 字符串 → Go `nil`（`*bool` / `*string`）；
- 百分比字符串（如 `"0.47%"`）保留原串（Score 字段），由前端展示；
- 空串/`"N/A"` 保持原样，不丢字段；
- `Media` 各服务含 `Status`（Yes/No/Block/Partial…）、`Region`、`Type`（Native/…）三字段，脚本已按服务动态生成，Go 侧不再硬编码服务清单；
- `Raw` 保留每个家族的规范化副本（含 Head.IP 完整 IP）。

## Panel / 前端报告表（`src/frontend/src/components/ServerTestPanel.tsx`）

重写 `IPQualityReport` 区块，按新结构渲染（分 IPv4/IPv6 两节）：

1. 基础信息卡：完整 IP、脚本版本、检测时间、ASN/组织、城市/时区、注册地区、IP 类型（Geo-consistent 等）；
2. 风险评分表：各数据库评分（Score 全字段）；
3. 风险因子矩阵：数据库 × 因子（Proxy/Tor/VPN/Server/Abuser/Robot/CountryCode）；
4. 流媒体徽章：7 服务 × Status/Region/Type；
5. 邮局检测表：Port25 + 12 邮局服务连通性 + DNSBL 汇总（Total/Clean/Marked/Blacklisted）；
6. Type 表：Usage/Company per-db。

`ProviderTable` 按需调整以展示新字段；渲染前对 `IPQuality` 为 nil 的旧报告做空态兼容。

## 错误处理

- 脚本拉取失败且无缓存 → 测试失败（failed），错误信息说明；
- 依赖安装失败 → failed，列出缺失命令；
- 执行超时 → failed；
- JSON 解析失败（脚本升级导致 schema 变化）→ failed + 保留 stderr 尾部/原始 stdout 片段便于排障；`Raw` 字段使 panel 侧仍可展示原始内容；
- 缺 v4/v6 → 该家族 unavailable，整体状态按 `completed_with_errors` 或 succeeded 视另一家族而定。

## 测试策略

- 解析器单测（`ipquality` 子包）：
  - 官方样本 `res/output.json`（单栈 v4）；
  - 双栈样本（v4+v6 两个文档流式拆解）；
  - 缺家族样本（exit 4/6 映射）；
  - 边界：`"null"` 字符串、百分数、空字段、未知服务名新增。
- 脚本管理单测：版本比对、下载失败回退缓存、原子替换。
- 执行层单测：以假脚本（模拟 v4/v6 输出与退出码）验证命令参数与超时。
- 集成验证：真实环境跑一次 `-p -j -f` 对比 `res/output.json` 结构。
- 现有 e2e 脚本保持通过（`scripts/e2e/`）。

## 文档同步

- `docs/server-testing-design.md`：撤销"不下载或执行上游脚本"条款，改写 IP 质量部分为脚本化执行模型。
- 本设计文档归档于 `docs/superpowers/specs/`。
