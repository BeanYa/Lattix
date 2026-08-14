# 前端主题（设计语言）系统

前端支持多套设计主题运行时切换。主题由两个正交维度组成：

- **外观模式**（`light` / `dark`）：沿用 `<html>` 上的 `dark` class 与 `prefers` 语义，见 `lib/theme.tsx`。
- **设计主题**（`hig` / `cream` / …）：通过 `<html data-theme="<id>">` 激活，由本目录的注册表驱动。

默认主题是 **Apple HIG**（`src/frontend/src/index.css` 与 `src/styles/cream-grid.css` 的根令牌就是它的令牌）。

## 结构

```
src/themes/
├── types.ts       # ThemeDefinition 清单契约与插槽定义
├── registry.ts    # 已安装主题注册表（切换菜单由此驱动）
├── hig/           # 默认主题，仅清单，无覆写
└── cream/         # Cream Grid 主题（经典设计）
    ├── index.ts   # 清单：id / label / overrides
    ├── tokens.css # [data-theme="cream"] 令牌覆写（亮 + 暗）
    ├── dashboard.css
    └── DashboardCream.tsx
```

## 主题如何生效

1. **令牌层（必需）**：全站语义令牌（`--background`、`--cg-paper`、`--earth-*` 等）定义于
   `index.css` / `cream-grid.css` 的 `:root` 与 `.dark`。主题在同目录 `tokens.css` 中用
   `[data-theme="<id>"]` 与 `[data-theme="<id>"].dark` 覆写**同名令牌**即可让所有页面换肤，
   组件类无需改动。
2. **页面插槽（可选）**：若主题对某页面的 DOM 结构做了重写，在 `ThemeSlot`
   （`types.ts`）中声明插槽，并在清单 `overrides` 里给出懒加载组件。未声明时回落到
   `src/pages/` 下的基础实现。`pages/Dashboard.tsx` 是插槽分发的范例。

## 安装一个新主题

1. 新建 `src/themes/<id>/`（id 限小写字母开头、小写字母/数字/连字符）；
2. 编写 `tokens.css`（同名令牌覆写，含亮/暗两套）并在 `index.ts` 顶部 `import './tokens.css'`；
3. 如重写了页面标记，将组件与样式放入同目录，并在 `overrides` 声明插槽；
4. 在 `registry.ts` 的 `DESIGN_THEMES` 数组中注册清单。

完成后顶栏主题菜单（外观模式 + 设计主题）会自动列出该主题，选择即切换并持久化到
`localStorage`（`lattix-design-theme`）。卸载主题只需从数组移除，存储了失效 id 的客户端会
在下次加载时回落到默认主题。

## 约定

- 主题**不得**修改 `src/pages/` 基础实现来适配自己；差异一律收敛进主题目录。
- 页面级覆写组件保持与基础实现相同的 props/数据契约（自行取数），切换主题即整体替换。
- 令牌名是公共 API：基础样式新增令牌时，主题可在自己的 tokens.css 中按需补写。
