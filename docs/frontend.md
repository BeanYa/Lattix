# 前端开发

Lattix 前端位于 `src/frontend/`，使用 Vite、React、TypeScript 与 shadcn/ui，依赖由
Bun 管理。

```bash
cd src/frontend
bun install --frozen-lockfile
bun run dev      # 启动开发服务器
bun run build    # TypeScript 检查并生成生产构建
bun run lint     # 运行 oxlint
bun run preview  # 本地预览生产构建
```

生产构建产物位于 `src/frontend/dist/`。发布时，工作流会将其复制到
`src/backend/internal/web/dist/`，再嵌入 Go 面板二进制。
