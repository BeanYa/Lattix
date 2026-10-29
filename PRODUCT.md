# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Lattix 面向自行部署并维护代理基础设施的管理员。他们在桌面端控制面板中持续观察多台服务器、链路、用户、订阅与运行状态，并在异常发生时完成定位和恢复操作。单管理员使用，没有多角色权限体系。

## Product Purpose

Lattix 是一个多服务器 xray 代理管理面板。Backend 通过一条由 Agent 主动建立的 WebSocket 长连接统一管理受控服务器，让管理员能够可视化配置节点和链路、分配用户与订阅、观察运行状态并执行维护操作。成功意味着管理员能快速理解系统当前状态、发现异常，并安全完成日常变更。

## Positioning

Lattix 将多服务器代理编排、逐跳状态管理、订阅发布、运行监控和维护操作收拢在一个自托管控制面板中，并以 Agent 主动外连、热操作与失败回滚降低跨服务器管理成本。

## Operating Context

产品用于自托管 Linux 服务器环境。管理员主要在桌面浏览器中长时间使用控制台、运行监控、服务器、成本、链路、用户、分组、订阅与日志页面；移动端用于临时查看和执行有限操作。界面同时承载实时状态、密集表格、配置表单、长操作进度和异常信息。

现有页面与入口（来自 `src/frontend/src/App.tsx` 与 `src/frontend/src/components/nav-config.ts`）：登录、公开订阅页、控制台（原「仪表盘」）、服务器、链路、链路分组、用户、用户分组、订阅模板、外部订阅、运行监控、成本统计、操作日志、请求日志、设置。

## Capabilities and Constraints

产品能力（证据：`README.md`、`docs/framework-design.md`）：

- 单条 WebSocket 长连接承担全部双向通信，Agent 主动拨出，Backend 永不外连。
- 虚拟配置模板下发，Agent 落地为 xray 实际配置并上报生效值。
- 节点与用户增删为零重启热操作（xray gRPC API），重启兜底与失败回滚。
- 直连与中转（代理链 / NAT）链路、Reality 安全层、多格式订阅与分流模板。
- 流量统计、事件告警、成本统计、配置漂移 reconcile 与 xray 版本升级管理。

约束（用户确认，长期有效）：

- 界面不依赖外部字体或资源 CDN，核心界面与设计资源全部自托管。
- 浅色 / 深色外观是独立维度，两种外观都必须完整可用；默认外观为深色；首屏避免主题闪白。
- 表达性设计不能遮蔽任务、状态或熟悉的操作控件。
- 全站只保留一套设计语言，不提供设计主题选择器或主题注册表；外观维度仅保留浅色/深色切换。

## Brand Commitments

保留 Lattix 名称与现有标识（`src/frontend/src/components/LattixMark.tsx`）。除此之外，没有必须继承的视觉元素；新的设计语言可以完全独立。

## Evidence on Hand

- 产品能力与部署事实来自 `README.md`、`docs/framework-design.md`、`docs/openapi.yaml`。
- 前端结构见 `docs/frontend.md`；现行设计语言见根 `DESIGN.md` 与 `src/frontend/src/styles/tokens.css`。
- 现有 Lattix 标识组件位于 `src/frontend/src/components/LattixMark.tsx`。
- 仪表盘演示数据位于 `src/frontend/src/lib/dashboard-demo.ts`（`DEMO_DASHBOARD_STATS`、`DEMO_SERVERS`），可在无真实后端时呈现真实结构的演示内容，但不得当作真实客户或性能证据。
- 仓库没有可用于新增商业声明、客户背书或性能基准的证据，不得虚构。

## Product Principles

- 状态优先：重要状态、异常与进行中的操作必须能被快速扫描。
- 操作可信：高影响动作要保持清晰、可预测并有明确反馈。
- 密度有序：支持复杂基础设施管理，但不让信息变成视觉噪声。
- 自托管完整：核心界面与主题不依赖外部运行时资源。
- 渐进增强：桌面端提供完整工作台，窄屏仍保留关键查看与操作能力。
- 一种语言：翻新后全站只使用一套设计语言，不再以多主题并存作为设计前提。

## Accessibility & Inclusion

保持键盘导航、可见焦点、语义化状态提示、足够的色彩对比度，并尊重 `prefers-reduced-motion`。
