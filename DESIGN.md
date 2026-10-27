---
name: Lattix Liquid Glass
description: A warm dusk liquid-glass operations panel for self-hosted proxy infrastructure — frosted translucent surfaces over a glowing dusk field, copper accents, dark by default.
colors:
  ground: "#14100d"
  surface: "rgba(34, 26, 21, 0.58)"
  raised: "rgba(46, 36, 29, 0.62)"
  inset: "#171210"
  rule: "rgba(255, 255, 255, 0.1)"
  rule-strong: "rgba(255, 255, 255, 0.22)"
  edge: "rgba(255, 255, 255, 0.12)"
  edge-strong: "rgba(255, 255, 255, 0.24)"
  ink: "#f2eae0"
  ink-2: "#bcada0"
  ink-3: "#a19380"
  control: "#8a7d70"
  accent: "#c9804f"
  accent-ink: "#1d120b"
  accent-soft: "rgba(201, 128, 79, 0.16)"
  selection: "rgba(201, 128, 79, 0.16)"
  focus: "#d8915f"
  success: "#45c79b"
  warning: "#eda23d"
  danger: "#f06a7e"
  waiting: "#b8af9f"
  unknown: "#a79e90"
  popover: "#211a15"
  scrim: "rgba(8, 5, 3, 0.62)"
  light-ground: "#e8d2c4"
  light-surface: "rgba(255, 251, 246, 0.58)"
  light-raised: "rgba(255, 247, 238, 0.68)"
  light-inset: "#efdccb"
  light-rule: "rgba(122, 90, 64, 0.2)"
  light-ink: "#2b2320"
  light-ink-2: "#6b5b50"
  light-ink-3: "#74645a"
  light-accent: "#8f5329"
  light-accent-ink: "#fff4e8"
  light-popover: "#fffaf4"
  brand-ink: "#101508"
  brand-yellow: "#bdf33b"
  brand-mint: "#78d69e"
typography:
  display:
    fontFamily: "Manrope Variable, Geist Variable, Noto Sans SC, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "30px"
    fontWeight: 800
    lineHeight: 1.1
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "Manrope Variable, Geist Variable, PingFang SC, sans-serif"
    fontSize: "22px"
    fontWeight: 700
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Manrope Variable, Geist Variable, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "15px"
    fontWeight: 700
    lineHeight: 1.3
    letterSpacing: "-0.01em"
  body:
    fontFamily: "Manrope Variable, PingFang SC, Hiragino Sans GB, Microsoft YaHei, Noto Sans CJK SC, sans-serif"
    fontSize: "14px"
    fontWeight: 450
    lineHeight: 1.55
  label:
    fontFamily: "Manrope Variable, Noto Sans SC, PingFang SC, sans-serif"
    fontSize: "12px"
    fontWeight: 650
    lineHeight: 1.3
    letterSpacing: "0.03em"
  readout:
    fontFamily: "Manrope Variable, Geist Variable, PingFang SC, sans-serif"
    fontSize: "24px"
    fontWeight: 700
    lineHeight: 1.15
    letterSpacing: "-0.01em"
---

# Design System: Lattix Liquid Glass

## Overview

Lattix 的设计语言是**液态玻璃（liquid glass）**：整个面板浮在一片暖调晚霞渐变之上
（深色为夜幕铜晕，浅色为暖纸晚霞），所有模块是半透明磨砂玻璃卡——`blur(28px)
saturate(140%)` 背景、1px 半透明边缘高光（edge）、20px 大圆角、深而柔的阴影。
铜橙是唯一强调色，只用于当前项、主操作、选中与进行中；状态由状态色 + 状态词双表达。
默认外观为深色（夜幕玻璃），浅色（暖纸晚霞玻璃）是同一语言的日间版；
两种外观都完整可用，运行时不依赖任何外部字体或资源 CDN。

证据路径：令牌层 `src/frontend/src/styles/tokens.css`（深浅变量层）、
表面原语 `src/frontend/src/styles/panel.css`、页面原语与 shadcn 外观
`src/frontend/src/index.css`、应用壳 `src/frontend/src/components/AppShell.tsx`。

## Colors

### Primary

- 铜橙 `--panel-signal`（深 `#c9804f` / 浅 `#8f5329`）：主按钮、链接、焦点、选中、进行中。
- `--panel-signal-ink`：铜底上的文字（深 `#1d120b` / 浅 `#fff4e8`）。
- `--panel-signal-soft`：选中与当前项的软铜底。

### Secondary

- 状态三色：通行 `--panel-status-ok`、降级 `--panel-status-degraded`、异常 `--panel-status-failed`；
  等待/未知用暖灰。状态色 + 状态词 + 状态点三冗余，不得以颜色单独表达状态。

### Tertiary

- 线路身份色 `--panel-line-0..8`（朱砂/琥珀/橄榄/苔绿/青瓷/紫藤/品红/赭石/石墨），
  按 id % 9 稳定分配，只出现在链路徽标与链路序列。

### Neutral

- 地面 `--panel-ground`：深 `#14100d`、浅 `#e8d2c4`——渐变基色，实际视觉由壳层晚霞
  渐变提供（`panel.css` 壳规则）。
- 表面 `--panel-surface` / 浮起 `--panel-raised`：半透明玻璃色；`--panel-inset` 为实色读数底。
- 文字 `--panel-ink / ink-2 / ink-3`：暖白三级阶梯。

### Named Rules

- 边缘与分隔分离：玻璃卡描边用 `--panel-edge`（半透明白高光），内容分隔线用
  `--panel-rule`（深色白 10%，浅色暖棕 20%）。
- 弹层使用实色 `--panel-popover`，不透字。
- 新颜色必须先成为 `tokens.css` 令牌并通过 `check:tokens` 对比度门禁（4.5:1，
  正文与状态词对 surface/raised）。

## Typography

- 标题与正文：`--font-title/--font-ui`（Manrope Variable 本地打包，Geist Variable 与
  系统 CJK 栈兜底）。
- 数字：一律 `--font-readout`（Manrope）+ `tabular-nums`；不再使用窄体读数字。
- 代码/日志：`--font-mono`（系统等宽）。

### Hierarchy

- 页面标题 `.page-title` 30px/800；模块标题 `.module-title` 13px/700；
  正文 `--type-body`；读数 `--type-readout` 系列（24px/700）。

### Named Rules

- 中文正文保持系统 CJK 栈渲染；数字永远 tabular；英文缩写（LATTIX、ACTIVE）保持原样。

## Layout

- 壳：顶部 56px 命令栏（玻璃）+ 左导轨（<1024 收起，1024–1279 图标栏，≥1280 全宽）
  + 主列独立滚动（最大 1600px，底部为 dock 预留 `pb-28`）。
- 底部居中玻璃停靠栏（dock）：五个一级入口图标，当前入口下方铜橙圆点，
  悬停图标按光标距离缩放。
- 页面骨架 `.page-shell` 网格 gap 14px；页头 `.page-header` 底部 1px rule。

### Named Rules

- 侧栏、顶栏、dock 与玻璃卡共享 `backdrop-filter: var(--panel-backdrop)`；
  玻璃只作用一层表面，嵌套内容保持实色。

## Elevation & Depth

- 深度由三层表达：晚霞渐变地面 → 半透明玻璃表面 → 黑系柔和阴影。
- 弹层（对话框/下拉/选单）使用实色 `--panel-popover` + overlay 阴影，不透字。

### Shadow Vocabulary

- `--panel-shadow-card`：玻璃卡阴影（深色黑系，浅色暖棕系）。
- `--panel-shadow-overlay`：弹层阴影，更重更近。

### Named Rules

- 目标进度条等强调元素允许一圈铜色辉光（`0 0 18px rgba(231,164,113,.28)`），
  这是全站唯一的辉光。

## Shapes

- 圆角阶梯：`--radius-xs/sm/md/lg = 6/10/14/20px`，`--radius-pill = 999px`；
  tailwind `--radius-*`（6/10/14/18…28）同步放大。

### Named Rules

- 卡/面板 20px，控件 10px，徽标与计数 6px，胶囊 999px；不再出现 2–4px 锐角。

## Components

### Buttons

- `.cg-button`：玻璃底、`--panel-control` 描边、10px 圆角；
  `.is-primary` 铜底 `--panel-signal-ink` 字；悬停浮起用 `--panel-raised`。

### Chips

- `.cg-status/.cg-pill`：描边 + 状态点 + 状态词三冗余；当前项 `is-active` 为铜底。

### Cards / Containers

- `.cg-panel/.cg-card/.surface-frame/[data-slot=card]`：玻璃 + edge 描边 + 20px 圆角。
- 表格容器 `[data-slot=table-container]` 保持实色可读，不加 blur。

### Inputs / Fields

- 玻璃底、rule 描边、10px 圆角；焦点环 2px `--panel-focus`。

### Navigation

- 侧栏当前项：`--panel-signal-soft` 软铜底 + 铜字 + 左侧 2px 铜条；分组标题淡色。
- dock：玻璃胶囊、图标缩放跟随光标距离（尊重 `prefers-reduced-motion`），
  当前入口铜点。
- ⌘K 命令面板：玻璃面（`--panel-popover` 实色 + 阴影），匹配文字铜橙高亮。

### Named Rules

- 模块标题前的 6px 铜橙方块索引（`.module-title::before`）保留，颜色跟随 signal。

## Do's and Don'ts

### Do:

- **Do** 用 `tokens.css` 令牌写样式，任何新颜色先进令牌并过 `check:tokens` 门禁。
- **Do** 让玻璃卡叠在晚霞渐变上：surface 半透明 + `--panel-backdrop`；把实色留给
  inset/代码块/表格容器。
- **Do** 数字一律 tabular；中文文案保持中文；状态用状态色 + 状态词双表达。
- **Do** 保持键盘焦点环、`prefers-reduced-motion` 降级与深浅两套外观的完整可用。
- **Do** 让图表从 CSS 变量取色（`chartThemeColors()`），跟随外观切换。

### Don't:

- **Don't** 引入第二强调色；铜橙之外只有状态色与线路身份色。
- **Don't** 在玻璃卡内再叠玻璃（嵌套 blur 只保留一层表面深度）。
- **Don't** 把弹层做成半透明——弹层用 `--panel-popover` 实色。
- **Don't** 用状态色或线路色做装饰；辉光只属于目标进度条。
- **Don't** 依赖外部字体/资源 CDN；字体一律本地打包（@fontsource）。
- **Don't** 恢复纸纹纹理、锐角 2–4px 或红/蓝信号色——它们属于已被替换的
  「值班台/Line Board」语言。
- **Don't** 遮挡任务与状态：玻璃不得降低正文与表格对比度（门禁 4.5:1）。
- **Don't** 重新引入多设计语言并存；只有外观（深/浅）这一个可切换维度。
