# 订阅分流与模板

本文描述 Lattix 的订阅策略、模板缓存、用户快照和公开读取协议。Lattix 不接收或转换外部节点订阅，也不实现 subconverter。模板只提供分组和分流策略，节点、用户凭据与链路端点始终来自 Lattix 自己的数据库和已发布链 revision。

## 1. 管理流程

1. 创建或编辑用户时，在“订阅设置”选择建议规则或自定义模板。
2. 建议规则可选 Minimal、Balanced、Comprehensive，并可继续勾选具体分类。Balanced 是默认值。
3. 自定义模板选择一份 Lattix 中立 YAML 或 ACL4SSR INI。Mihomo、sing-box、Quantumult X 原生模板可作为单客户端覆盖。
4. 保存设置后一次生成所有格式，写入不可变 snapshot，再原子切换该用户的 published 指针。
5. 用户访问订阅地址时只读取 published snapshot，不执行模板下载、规则下载或配置编译。

发布时收集已分配但未纳入订阅的链的原因（未发布有效修订 / 条目构造失败），随快照持久化
（`subscription_snapshots.warnings`，schema v10）。预览与"重新生成"API 返回 warnings，
用户页以"部分条目未纳入本次订阅"提示，不再静默丢弃已分配条目。

用户页“结果预览”读取的也是 published snapshot。模板页“模板预览”显示未填充的缓存原文。

## 2. 公开订阅协议

统一地址是 `GET /sub/{token}`：

| `format` | 内容 |
| --- | --- |
| `clash` | Mihomo YAML 完整配置 |
| `singbox` | sing-box JSON 完整配置 |
| `quanx` | Quantumult X 节点列表，保持原有 node-only 行为 |
| `quanx-config` | Quantumult X 完整分流配置 |
| `links` | Base64 分享链接集合 |

内容来源为受控链路节点与用户关联的外部订阅节点两类：外部节点经独立构建器输出
（mihomo YAML / sing-box JSON / Quantumult X / 分享链接，与解析器互逆），客户端格式
无法表达的协议跳过并记入快照 warning；详见 [framework-design §9.1](framework-design.md#91-外部订阅导入与用户关联)。

`clash` 输出为开箱即用配置：内置 fake-ip DNS（`enhanced-mode: fake-ip`、198.18.0.1/16、本地域名
fake-ip-filter，默认/DoH nameserver；节点域名经 `proxy-server-nameserver` 用国内可达解析器直查，
不设境外回退，避免节点测速超时）；策略包含 GEOSITE/GEOIP 规则时另输出 `geodata-mode` +
`geo-auto-update` 与 `geox-url`（MetaCubeX/meta-rules-dat），保证规则在客户端直接生效。

未传 `format` 时按 User-Agent 识别 Mihomo、sing-box、Quantumult X 或分享链接客户端。浏览器请求包含 `Accept: text/html` 时返回订阅落地页。

远程规则不会直接指向 GitHub。配置引用：

```text
GET /sub/{token}/rules/{source_sha256}/{mihomo|singbox|quanx}/{rule_name}
```

规则地址同时受订阅 token 保护，并按源内容 SHA-256 固定版本。响应包含 ETag、Last-Modified 和 `X-Lattix-Subscription-Revision`。

## 3. 快照生命周期

下列动作重新生成受影响用户的订阅：

- 创建用户、修改订阅设置、修改节点分配；
- 用户到期、恢复、停用或启用；
- 管理员手动重新生成；
- 已分配节点收到生效 ACK，或节点/链被删除；
- 链 revision 正式发布；
- 服务器别名、地址、标签、国家或位置变化。

节点遥测、延迟、流量变化和模板刷新不触发重新生成。后台队列按用户去重并合并短时间内的重复事件。

每个 snapshot 同时保存 `clash`、`singbox`、`quanx`、`quanx-config`、`links` 和引用的客户端原生规则制品。任何文件生成或数据库写入失败时，事务回滚，published 指针仍指向上一 revision。停用或到期用户会发布节点为空的新 snapshot。

## 4. 模板来源与刷新

模板类型：

- `portable`：Lattix 中立策略 YAML；
- `acl4ssr`：ACL4SSR INI 的分流子集；
- `mihomo`：Mihomo 原生 YAML；
- `singbox`：sing-box 原生 JSON；
- `quanx`：Quantumult X 原生配置。

社区模板只接受公开 GitHub 具体文件的 HTTPS URL。`github.com/.../blob/...` 会规范化到 `raw.githubusercontent.com`。Aethersailor 模板中的 jsDelivr GitHub 镜像只用于识别作者仓库路径，实际抓取会改写到同一作者仓库的 raw GitHub 文本文件；`.mrs` 会选择作者仓库内对应的 `.yaml` 源文件，Lattix 不解析二进制规则。

新增或编辑 GitHub 模板时会立即下载并校验模板及所有引用规则，成功后才在一个事务中保存完整缓存；任一步失败都不会留下半成品或覆盖上一份可用缓存。启动时执行一次刷新，之后每 6 小时巡检，也可在面板手动刷新。后续刷新遵循相同的原子替换规则，失败只记录 `last_error`，继续使用上一份完整缓存。刷新模板缓存绝不修改已发布用户 snapshot。

内置和社区来源模板只读。编辑前需克隆为本地模板。

## 5. 中立 YAML

```yaml
name: Example
groups:
  - name: Proxy
    type: select
    options: [__LATTIX_REGIONS__, __LATTIX_ALL__, DIRECT]
rules:
  - kind: DOMAIN-SUFFIX
    value: example.com
    outbound: Proxy
remote_rules:
  - name: example
    url: https://raw.githubusercontent.com/owner/repo/main/example.list
    behavior: classical
    outbound: Proxy
final: Proxy
```

分组类型支持 `select`、`url-test`、`fallback`、`load-balance`。规则必须引用已声明分组或 `DIRECT`、`REJECT`。远程规则名称只能包含字母、数字、点、下划线和连字符。

## 6. 原生模板占位符

Mihomo `proxy-groups[].proxies` 和 sing-box selector `outbounds` 支持：

- `__LATTIX_ALL__`：全部可用节点；
- `__LATTIX_REGIONS__`：动态叶子分组（来源分组 + 地区分组 + 无地区分组）；
- `__LATTIX_REGION_XX__`：ISO 3166-1 alpha-2 国家对应的节点，例如 `__LATTIX_REGION_US__`。

发布时另按节点来源自动生成分组（无节点的来源不生成，与既有组重名时跳过）：「<面板缩写> 分组」包含全部面板管理链路节点（面板缩写取设置 `panel_short`，默认 `Lattix`），每个外部订阅一组（组名 = 订阅名）包含其解析节点；外部订阅节点的地区归属由节点名称推断（旗帜 emoji 优先，其次地区关键词），无法推断的节点收进「🌐 无地区」分组。来源分组、地区分组与无地区分组合称叶子分组（只含节点、不引用其他组），三者同级，随 `__LATTIX_REGIONS__` 一同展开、一同剔除。

可达性增强：模板定义的分流组（地区组除外）在保留原有选项的基础上追加全部节点与「自动选择」「节点选择」（final）引用，使任何分流组既可指定固定节点也可回落自动选择；节点池组（选项含 `__LATTIX_ALL__`，如自动选择/故障转移）与 final 组的选项仅保留节点与叶子分组（及 DIRECT/REJECT），剔除其他组引用——它们因此永不反向引用分流组，注入不会产生引用环，发布前另有引用环检测兜底。

Quantumult X 原生模板必须包含 `__LATTIX_SERVERS__`，发布时替换为 `[server_local]` 节点内容。Mihomo 的 `proxies` 和 sing-box 的节点 outbounds 始终由 Lattix 结构化注入，不使用字符串拼接。

链节点归属出口服务器国家，连接地址和端口仍取链入口。共享 Endpoint 链使用对应 assignment 的 `access_uuid`，独立节点继续使用用户 `uuid`。

## 6.1 预编译中间态

引入中间态的目的：保证**同类节点在订阅文件生成过程中始终作为整体处理**——面板管理链路节点、各外部订阅节点在处理管线中分别以整体占位符出现，分组/展开/剪枝等所有环节只见到「这一类节点」而非逐条节点载荷，直到最后的渲染（交付）阶段才把占位符替换回真实的节点属性与目标格式。这样节点与分组可以同级或整体地参与处理，个别节点的新增、失效或属性变化不会影响处理逻辑。

订阅文件生成分三层：先在**中立策略层**完成全部逻辑处理（选项展开/分组增强/剥离/剪枝/环检测——这些环节只操作名字，不需要节点载荷；来源分组在这一层保持具体，否则剪枝会把无选项的占位符组误判为死组），再构建**格式中间态**（节点区与来源分组区以占位符标出位置，载荷随结构在构建期捕获），最终交付前解压为真实条目（clash 为含 name/type/server/port 的完整 proxy map，sing-box 为 outbound 对象，QuanX 为 server 行 / policy 行，links 为分享链接）。条目级占位符：

- `__LATTIX-NODES__`：面板管理链路节点条目；
- `__OUTTER-SUBS-NODES__`：外部订阅节点条目（用户分配到的全部外部订阅节点）；
- `__LATTIX-GROUP__`：「<面板缩写> 分组」条目；
- `__OUTER-SUBS-GROUP__`：外部订阅分组条目，解压为每组一个（3 条外部订阅即 3 个分组）。

原生模板作者可手写上述占位符控制注入位置（例如让外部订阅节点排在面板节点之前）；未写时按默认规则注入，行为与此前一致。空来源（无面板节点/无外部订阅）的占位符条目在解压时删除，不产出空分组。交付内容不允许残留占位符，残留即发布失败。

每个 snapshot 除正式文件外另存 `<format>-pre` 中间态文件（同 snapshot 级联清理，公共订阅端点不可达），面板用户订阅预览对话框可切换「正式交付 / 预编译中间态」查看（`GET /api/user/subscription-preview` 加 `stage=pre`）。

## 7. ACL4SSR 支持范围

支持 `[custom]` 中影响路由的：

- `custom_proxy_group`；
- `ruleset` 的 `[]FINAL`、`[]GEOSITE,<name>`、`[]GEOIP,<name>`；
- GitHub 文本规则 URL；
- Aethersailor 使用的 `clash-domain:`、`clash-classic:`、`clash-ipcidr:` GitHub 文件前缀；
- `rules/<owner-or-repo>/...` 相对路径，仅解析到模板自身的 GitHub 仓库与同一 ref；
- `enable_rule_generator` 与 `overwrite_original_rules`，二者在 Lattix 中无需额外动作。

普通 subconverter 输入、重命名和订阅合并参数不会影响 Lattix 路由，因而忽略。未知且名称包含 `rule` 或 `proxy_group` 的指令会带原始行号拒绝，防止静默漏掉路由语义。

规则制品支持 DOMAIN、DOMAIN-SUFFIX、DOMAIN-KEYWORD、DOMAIN-REGEX、IP-CIDR、IP-CIDR6、SRC-IP-CIDR、GEOIP、GEOSITE、PROCESS-NAME、PROCESS-PATH、USER-AGENT、URL-REGEX、DST-PORT、SRC-PORT 和 IP-ASN。sing-box 没有等价的 URL 层正则，`URL-REGEX` 会降级为 `domain_regex`。

## 8. 安全限制

- 模板和规则下载使用项目统一 `requester`，限制响应体大小和请求超时；
- 不接受 HTTP、任意站点 URL、目录 URL 或非 GitHub 社区源；
- Mihomo 原生模板拒绝 external controller、secret、authentication 和 script；
- sing-box 原生模板拒绝对外监听的 Clash API 和 V2Ray API；
- Quantumult X 原生模板拒绝 `[http_backend]` 与 `resource_parser_url`；
- 只读模板不能通过直接 API 请求覆盖，正在被用户引用的模板不能删除。

## 9. 归属

内置社区源直接引用作者公开仓库，不包含或复制其项目代码：

- [ACL4SSR/ACL4SSR](https://github.com/ACL4SSR/ACL4SSR)，CC BY-SA 4.0；
- [Aethersailor/Custom_OpenClash_Rules](https://github.com/Aethersailor/Custom_OpenClash_Rules)，CC BY-SA 4.0。

面板记录每个模板的来源 URL、许可证、正文 SHA-256、抓取时间和最后错误。发布配置头部也标明由 Lattix 生成。
