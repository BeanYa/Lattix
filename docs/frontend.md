# 前端开发

Lattix 前端位于 `src/frontend/`，使用 Vite、React、TypeScript 与 shadcn/ui，依赖由
Bun 管理。

```bash
cd src/frontend
bun install --frozen-lockfile
bun run dev           # 启动开发服务器
bun run build         # TypeScript 检查并生成生产构建
bun run lint          # 运行 oxlint
bun run format        # Prettier 格式化
bun run format:check  # Prettier 校验（CI 强制执行）
bun run preview       # 本地预览生产构建
```

生产构建产物位于 `src/frontend/dist/`。发布时，工作流会将其复制到
`src/backend/internal/web/dist/`，再嵌入 Go 面板二进制。

## 结构约定

- `src/lib/` 为跨页共享设施；展示辅助集中归并：`use-polling.ts`（统一的周期轮询
  hook）、`status.ts`（状态枚举到展示样式/文案的映射）、`format.ts`
  （humanizeBytes/formatBytes/CURRENCIES 等格式化助手）。
- `src/pages/` 每页一个入口组件；较大页面的表单对话框与配套 hook 拆入同名子目录
  （`chains/`、`servers/`、`settings/`、`users/`），入口文件只保留页面骨架。

## 主题系统

前端支持多套设计主题运行时切换，由两个正交维度组成：

- **外观模式**（浅色 / 深色）：`<html>` 上的 `dark` class，`lib/theme.tsx` 持久化到
  localStorage（`lattix-theme`）。
- **设计主题**（`hig` / `cream` / …）：`<html data-theme="<id>">`，持久化到
  `lattix-design-theme`，由 `src/frontend/src/themes/registry.ts` 注册表驱动。

默认主题是 **Apple HIG**（`index.css` 与 `styles/cream-grid.css` 的根令牌就是它的
令牌）；**Cream Grid**（重构前的经典设计）作为可选主题安装，含全量令牌覆写与
Dashboard 页面覆写。顶栏调色板按钮可同时切换两个维度，`index.html` 中的首屏脚本会在
渲染前应用持久化的主题以避免闪白。

主题 = 一个 `src/frontend/src/themes/<id>/` 目录 + 注册表中的一条记录：

- `tokens.css` 用 `[data-theme="<id>"]` / `[data-theme="<id>"].dark` 覆写**同名语义令牌**
  即可让全站换肤，组件类零改动；
- 若主题重写了某页面的 DOM 结构，在清单 `overrides` 中声明插槽（当前支持
  `dashboard`），对应页面入口（如 `pages/Dashboard.tsx`）会懒加载主题实现，否则回落
  基础实现。

安装与卸载主题的完整步骤和约定见
[`src/frontend/src/themes/README.md`](../src/frontend/src/themes/README.md)。

## CDN 与缓存

生产环境可以把面板域名开启 Cloudflare Proxy（橙云），回源到 Lattix 面板。不要把
React、Three.js 等依赖改成多个公共包 CDN URL：完整 Vite 构建产物版本一致、可回滚，
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
