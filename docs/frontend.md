# 前端开发

Lattix 前端位于 `src/frontend/`，使用 Vite、React、TypeScript 与 shadcn/ui，依赖由
Bun 管理。

```bash
cd src/frontend
bun install --frozen-lockfile
bun run dev           # 启动开发服务器
bun run build         # TypeScript 检查并生成生产构建
bun run test          # 运行 vitest 单元测试
bun run lint          # 运行 oxlint
bun run format        # Prettier 格式化
bun run format:check  # Prettier 校验（CI 强制执行）
bun run check:tokens  # 令牌深浅两套 WCAG 2.2 AA 对比度门禁
bun run check:budget  # 构建产物首屏体积预算检查（需先 build）
bun run generate:api  # 由 docs/openapi.yaml 生成 API 类型（src/lib/api-contract.generated.ts）
bun run check:api     # 校验生成的 API 类型与 OpenAPI 定义一致（build 前置步骤）
bun run preview       # 本地预览生产构建
```

生产构建产物位于 `src/frontend/dist/`。发布时，工作流会将其复制到
`src/backend/internal/web/dist/`，再嵌入 Go 面板二进制。

## 结构约定

- 登录后的实际页面壳为 `components/AppShell.tsx`；全局 `UpdateOverlay` 必须在此挂载，负责面板更新进度、重启恢复后的目标版本确认和倒计时整页刷新。关闭失败结果后会恢复监听，以便再次更新。
- `src/lib/` 为跨页共享设施；展示辅助集中归并：`use-polling.ts`（统一的周期轮询
  hook）、`status.ts`（状态枚举到展示样式/文案的映射）、`format.ts`
  （humanizeBytes/formatBytes/CURRENCIES 等格式化助手）。
- `src/pages/` 每页一个入口组件；较大页面的表单对话框、标签页与配套组件/hook
  拆入同名子目录（`chains/`、`runtime/`、`servers/`、`settings/`、
  `subscription/`、`users/`），入口文件只保留页面骨架。

## 设计系统与外观

前端只有一套设计语言「液态玻璃」（权威定义见根 `DESIGN.md`，Lattix Liquid
Glass），不再提供设计主题切换；外观模式是唯一可切换维度：

- **深色（默认，夜幕玻璃）** / **浅色（暖纸晚霞玻璃）**：`<html>` 上的 `dark` class，
  `lib/theme.tsx` 持久化到 localStorage（`lattix-theme`），`index.html` 的首屏脚本在渲染
  前应用，避免闪白。
- 令牌层：`src/styles/tokens.css` 定义深浅两套语义令牌（`--panel-ground`、
  `--panel-surface`（半透明玻璃）、`--panel-edge`（卡描边）、`--panel-rule`（内容分隔）、
  `--panel-signal`（铜橙强调）、状态色、字体与圆角阶梯），并新增 `--panel-backdrop`
  （`blur(28px) saturate(140%)`）与 `--panel-popover`（弹层实色）；旧页面的 `--cg-*`
  名称在 `src/styles/panel.css` 中保留为同一套令牌的别名。
- 表面层：`src/styles/panel.css` 提供玻璃原语（`.cg-panel`、`.cg-card`、`.cg-button`、
  `.cg-status` 等共享组件类）与壳层晚霞渐变背景；卡面为半透明表面 + 背景 blur +
  edge 描边 + 20px 圆角。
- 组件层：`src/index.css` 提供页面原语（`.page-shell`、`.page-header`、`.page-title`、
  `.surface-frame`、`.notice`、`.empty-state` 等）与 shadcn 组件的统一外观。
- 新页面必须使用令牌，不得硬编码颜色、圆角或阴影；铜橙只用于当前项、主操作、选中与
  进行中，状态由状态色 + 状态词双表达；深浅两套外观都要检查（`bun run check:tokens`
  为对比度门禁）。
- 字体与图标全部本地打包（`@fontsource-variable/manrope`、`@fontsource-variable/geist`、
  `flag-icons`、`lucide-react`），不依赖外部字体或资源 CDN。
- 应用壳（`components/AppShell.tsx`）：顶部命令栏与左导轨玻璃化外，底部另有玻璃
  停靠栏（dock），承载五个一级入口的图标快捷切换，当前入口有铜橙圆点。

## CDN 与缓存

生产环境可以把面板域名开启 Cloudflare Proxy（橙云），回源到 Lattix 面板。不要把
React 等依赖改成多个公共包 CDN URL：完整 Vite 构建产物版本一致、可回滚，
也不会因某个公共镜像不可用导致页面白屏。

Cloudflare 标准网络覆盖全球，但中国大陆访问属于尽力而为，不承诺境内节点与稳定时延。
如果大陆加速是强 SLA，需使用 Cloudflare China Network（企业方案，按要求完成备案），
或另设境内 CDN。以下源站缓存策略对 Cloudflare 标准网络与 China Network 都适用。

源站已经输出以下缓存语义，CDN 应选择“遵循源站 Cache-Control”：

| 路径 | CDN 行为 | 源站缓存头 |
| --- | --- | --- |
| `/assets/*` | 缓存并启用 Brotli/Gzip、HTTP/2/3 | `public, max-age=31536000, immutable` |
| `/`, SPA 路由及 `index.html` | 每次重新验证 | `no-cache` |
| `/api/*`、`/sub/*` | 不做公共缓存，直接回源 | 订阅公开 API 为 `private, no-store` |
| `/api/agent/ws` | WebSocket 透传 | 不缓存 |

Cloudflare Dashboard 建议配置：

1. DNS 中为面板域名开启 Proxy status。
2. 新建最高优先级 Cache Rule：URI Path starts with `/assets/`，Cache eligibility 设为
   Eligible for cache，Edge TTL 使用源站 Cache-Control；开启 Tiered Cache、Brotli 与 HTTP/3。
3. 新建 Bypass Cache Rule：URI Path starts with `/api/` OR `/sub/`。不要对这两个路径
   使用 Cache Everything。
4. 其他路径保持默认缓存行为；HTML 的 `no-cache` 会保证面板升级后及时获取新入口文件。
5. Network 中启用 WebSockets，确保 `/api/agent/ws` 正常透传。

不要为 `/sub/*` 配置“忽略源站强制缓存”。订阅地址本身是凭证，公开边缘缓存会造成
用户数据泄露。城市数据、字体、3D 与图表依赖都位于带内容哈希的 `/assets/*` 下，首次
下载后可长期复用；新版本文件名变化时会自然换新，无需清理旧缓存。
