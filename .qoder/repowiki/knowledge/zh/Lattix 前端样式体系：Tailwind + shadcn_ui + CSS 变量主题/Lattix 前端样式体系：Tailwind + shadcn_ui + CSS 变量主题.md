---
kind: frontend_style
name: Lattix 前端样式体系：Tailwind + shadcn/ui + CSS 变量主题
category: frontend_style
scope:
    - '**'
source_files:
    - src/frontend/package.json
    - src/frontend/vite.config.ts
    - src/frontend/src/index.css
    - src/frontend/components.json
    - src/frontend/src/lib/theme-context.ts
    - src/frontend/src/lib/theme.tsx
    - src/frontend/src/components/ui/button.tsx
    - src/frontend/tsconfig.json
---

## 1. 使用的系统与工具
- **构建与开发**：Vite 8 + React 19，通过 @vitejs/plugin-react 和 @tailwindcss/vite 插件集成。
- **样式框架**：Tailwind CSS 4（通过 @import "tailwindcss" 引入），配合 tw-animate-css 提供动画类。
- **UI 组件库**：shadcn/ui（style: base-nova）+ Base UI (@base-ui/react) 作为底层无样式原子组件，使用 class-variance-authority (cva) 管理变体。
- **图标与字体**：Lucide React 图标；Geist Variable 主字体 + Fusion Pixel 10px Proportional SC 标题/像素字体。
- **辅助库**：clsx + tailwind-merge 用于 className 合并；flag-icons 国旗图标；qrcode 二维码生成。

## 2. 核心文件与位置
- src/frontend/package.json：依赖与脚本定义
- src/frontend/vite.config.ts：Vite 配置、路径别名 @/*、开发代理 /api → localhost:8080
- src/frontend/src/index.css：全局样式入口，Tailwind + shadcn + 自定义 CSS 变量主题
- src/frontend/components.json：shadcn/ui 配置文件（base-nova 风格、CSS 变量模式、lucide 图标）
- src/frontend/src/lib/theme-context.ts / theme.tsx：主题上下文与 Provider，支持 light/dark 切换并持久化到 localStorage
- src/frontend/src/components/ui/*.tsx：shadcn 生成的基础 UI 组件（button、card、dialog、table 等）
- src/frontend/tsconfig.json：TypeScript 路径映射 @/* → ./src/*

## 3. 架构与设计约定
### 3.1 样式方法论
- **Tailwind CSS 4 原子类优先**：所有样式通过 Tailwind 实用类组合，避免手写 CSS。
- **CSS 变量驱动主题**：在 index.css 的 :root 和 .dark 中定义全部设计令牌（颜色、圆角、阴影等），通过 @theme inline 暴露为 Tailwind 变量。
- **shadcn/ui 组件 + cva 变体**：每个 UI 组件用 cva() 声明 variant/size 变体，通过 data-slot 属性选择器在 CSS 层统一增强样式（如按钮阴影、输入框边框）。
- **Base UI 作为无障碍基础**：底层交互逻辑由 @base-ui/react 提供，确保可访问性。

### 3.2 主题系统
- 通过 ThemeProvider 包裹应用根节点，根据系统偏好或 localStorage 中的 lattix-theme 决定初始主题。
- 切换时向 <html class="dark"> 添加/移除类名，并设置 colorScheme。
- 所有颜色通过 CSS 变量（--primary、--background、--destructive 等）统一管理，light/dark 两套变量值。

### 3.3 组件组织
- components/ui/：基础原子组件（button、input、dialog、table、tabs、badge、progress 等）
- components/：业务级复合组件（Layout、ThemeToggle、GlobeTopology、ServerMonitor 等）
- lib/：工具函数、API 客户端、状态管理、类型定义等横切关注点
- pages/：页面级路由组件（Dashboard、Chains、Servers、Settings、Users、Logs 等）

### 3.4 响应式与动效
- 使用 Tailwind 响应式前缀（sm/md/lg/xl）处理不同屏幕尺寸。
- 通过 tw-animate-css 提供过渡动画，自定义 page-enter、node-pulse 等关键帧。
- 尊重 prefers-reduced-motion，自动禁用非必要动画。

## 4. 开发者应遵循的规则
1. **优先使用 Tailwind 原子类**，不要新增手写 CSS；如需扩展，通过 @layer components 或 CSS 变量。
2. **使用 shadcn/ui 组件**，通过 cva 声明新变体，保持样式一致性。
3. **主题色必须走 CSS 变量**，禁止硬编码颜色值；新增颜色需在 :root 和 .dark 中成对定义。
4. **组件命名**：基础组件放 components/ui/，业务组件放 components/，页面放 pages/。
5. **路径别名**：统一使用 @/ 前缀引用 src 下模块，避免相对路径嵌套。
6. **图标统一使用 Lucide**，通过 lucide-react 导入。
7. **表单与交互**：优先使用 Base UI 提供的无障碍组件，再叠加 shadcn 样式。
8. **动画**：优先使用 tw-animate-css 类，复杂动画在 index.css 的 @keyframes 中定义。
9. **深色模式**：通过 dark: 前缀适配，确保所有组件在 dark 模式下可用。
10. **类型安全**：组件 props 使用 TypeScript 严格类型，避免 any。