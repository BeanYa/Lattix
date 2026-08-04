# Changelog

本项目所有显著变更记录于此。格式基于
[Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循
[Semantic Versioning](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Breaking changes

- `GET /api/billing/stats`（成本统计）响应字段改名：`costs_public` →
  `actual_costs_public`、`costs_custom` → `actual_costs_custom`、
  `totals_public` → `actual_totals_public`、`totals_custom` →
  `actual_totals_custom`。外部 API 消费者需同步更新。

### Added

- 新增 `GET /api/billing/stats/estimated`：对启用统计计费且未过期的服务器，按
  日成本 × 周期天数（日 1 / 月 30 / 年 365）估算每周期成本。
- 成本统计页新增「计算成本」tab：估算日/月/年成本汇总卡片、周期分布图与明细矩阵；
  原统计口径更名为「已生效成本」tab。
- 新增外部订阅管理页：导入第三方订阅 URL（base64 分享链接 / Clash-mihomo YAML /
  v2rayN 自定义格式），解析节点保存到外部链路表，订阅信息（流量/到期/节点数）保存到
  外部订阅表，支持手动与定时同步（每订阅可配间隔）。
- 用户可引入外部订阅并分配（叠加/并入/附加三种模式），订阅流量合并计入用户用量
  统计，新增外部订阅分配 RPC（设置/查询/关联用户级联清理）。
- 外部订阅节点字段保真：clash/mihomo YAML 输出覆盖完整 proxy schema（alpn、
  plugin-opts、smux、h2-opts、fragment、ipv6、anytls idle-session 系列等），
  `skip-cert-verify: false` 等 falsy 值按「字段存在」回填；schema 之外的未知/
  扩展字段（如第三方认证参数）原样透传；sing-box 出站补 alpn/idle-session/
  plugin/ipv6 映射；分享链接抑制已消费的 YAML 键（servername/client-fingerprint
  等）并透传未知标量参数。

### Fixed

- 计算成本（`GET /api/billing/stats/estimated`）折算模型改为「12 个月 × 30 天」口径：
  - 日成本按计费周期推导：年付 = 年付价 ÷ 周期年数 ÷ 12 ÷ 30，月付 = 月付价 ÷ 周期
    月数 ÷ 30，季付 = 季付价 ÷ 3 ÷ 30；月成本 = 日成本 × 30（年付 = 年付价 ÷ 12），
    年成本 = 日成本 × 360（月付 = 月付价 × 12、季付 = 季付价 × 4）
  - 自定义范围总成本 = 年成本 × 整年数 + 月成本 × 整月数 + 日成本 × 剩余天数
    （整月 30 天、整年 360 天），12 个月视图精确还原年价（如 99 CNY/年 12 个月
    合计 97.68 → 99.00）
  - 响应新增 `monthly_minor` / `annual_minor`（及 custom 变体），前端服务器汇总表
    标注折算公式、汇总卡片改为 ×360
- 外部订阅列表/节点列表为空时返回 `[]` 而非 `null`，修复外部订阅页与用户页打开
  新建/编辑弹窗时的 `Cannot read properties of null (reading 'length')` 崩溃。
- custom 换算模式下，无自定义锚点服务器的周期成本以 public 回退值计入 custom 合计，
  合计列 = 可见单元格之和。
- 外部订阅导入：默认 UA 改用 clash-meta 并支持 UA 预设，导入错误直接显示在对话框。
- 外部订阅节点数改为按实际解析内容统计。
