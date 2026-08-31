# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Lattix 面向自行部署并维护代理基础设施的管理员。他们在桌面端控制面板中持续观察多台服务器、链路、用户、订阅与运行状态，并在异常发生时完成定位和恢复操作。

## Product Purpose

Lattix 是一个多服务器 xray 代理管理面板。Backend 通过一条由 Agent 主动建立的 WebSocket 长连接统一管理受控服务器，让管理员能够可视化配置节点和链路、分配用户与订阅、观察运行状态并执行维护操作。成功意味着管理员能快速理解系统当前状态、发现异常，并安全完成日常变更。

## Positioning

Lattix 将多服务器代理编排、逐跳状态管理、订阅发布、运行监控和维护操作收拢在一个自托管控制面板中，并以 Agent 主动外连、热操作与失败回滚降低跨服务器管理成本。

## Operating Context

产品用于自托管 Linux 服务器环境。管理员主要在桌面浏览器中长时间使用仪表盘、运行监控、服务器、成本、链路、用户、分组、订阅与日志页面；移动端用于临时查看和执行有限操作。界面同时承载实时状态、密集表格、配置表单、长操作进度和异常信息。

## Capabilities and Constraints

- 保留现有 React SPA、路由、数据契约、中文文案、可访问语义及全部业务功能。
- 设计主题与浅色/深色外观是正交维度；新增主题必须通过现有注册表和 `data-theme` 机制安装，不能取代或破坏已有主题。
- 主题不得依赖外部字体或资源 CDN。
- 表达性设计不能遮蔽任务、状态或熟悉的操作控件。

## Brand Commitments

保留 Lattix 名称与现有标识。除此之外，没有必须继承的视觉元素；新增主题可以建立完全独立的视觉语言。

## Evidence on Hand

- 产品能力与部署事实来自 `README.md`。
- 前端主题契约位于 `src/frontend/src/themes/README.md`、`registry.ts` 与 `types.ts`。
- 现有 Lattix 标识组件位于 `src/frontend/src/components/LattixMark.tsx`。
- 仓库没有可用于新增商业声明、客户背书或性能基准的证据，不得虚构。

## Product Principles

- 状态优先：重要状态、异常与进行中的操作必须能被快速扫描。
- 操作可信：高影响动作要保持清晰、可预测并有明确反馈。
- 密度有序：支持复杂基础设施管理，但不让信息变成视觉噪声。
- 自托管完整：核心界面与主题不依赖外部运行时资源。
- 渐进增强：桌面端提供完整工作台，窄屏仍保留关键查看与操作能力。

## Accessibility & Inclusion

保持键盘导航、可见焦点、语义化状态提示、足够的色彩对比度，并尊重 `prefers-reduced-motion`。
