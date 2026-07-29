---
kind: build_system
name: 构建与发布系统（Go 多模块 + Docker + GitHub Actions）
category: build_system
scope:
    - '**'
source_files:
    - go.work
    - Dockerfile
    - .github/workflows/release.yml
    - install.sh
    - src/frontend/package.json
    - scripts/latx.sh
    - scripts/latx-ag.sh
---

## 1. 构建系统与工具链
- **Go 工作区**：根目录 `go.work` 聚合 `src/agent`、`src/backend`、`src/shared` 三个 Go 模块，统一使用 Go 1.26。
- **前端构建**：`src/frontend` 使用 Bun + Vite + TypeScript，通过 `bun run build` 产出静态资源并嵌入到后端二进制中。
- **容器化**：`Dockerfile` 采用多阶段构建——第一阶段用 `oven/bun:1-alpine` 构建前端，第二阶段用 `golang:1.26-alpine` 编译 Go 二进制，最终产物运行在精简的 `alpine:3.22` 镜像中。
- **CI/CD**：`.github/workflows/release.yml` 在推送 `v*` 标签时触发，完成构建、测试、打包、E2E 验证、GitHub Release 发布以及多架构 Docker 镜像推送。

## 2. 关键文件与职责
- `go.work` / `go.work.sum`：Go 工作区声明，集中管理三个模块依赖。
- `Dockerfile`：多阶段构建入口，注入 `VERSION`、`GITHUB_REPO` ldflags，输出可执行文件并生成 OCI 镜像。
- `.github/workflows/release.yml`：完整的 release 流水线，包含 amd64/arm64 交叉编译、checksum 生成、artifact 上传、镜像构建与发布。
- `install.sh`：统一安装入口，根据 `--version` 或自动解析 latest release，动态拉取对应 tag 下的 `install-panel.sh` / `install-agent.sh` 子脚本执行。
- `scripts/latx.sh` / `scripts/latx-ag.sh`：面板与 Agent 的管理 CLI，release 时由 CI 注入版本号后随包分发。
- `src/frontend/package.json`：定义前端构建脚本（`build`、`generate:api`、`check:api`），API 类型由 OpenAPI 自动生成。

## 3. 架构与约定
- **版本注入**：Go 二进制通过 `-ldflags "-X main.version=${VERSION} -X main.githubRepo=${GITHUB_REPO}"` 注入版本信息；CI 中校验二进制 `-version` 输出与 tag 一致。
- **多架构支持**：CI 对 amd64 和 arm64 分别执行 `CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build`，产物命名含架构后缀。
- **前端嵌入**：Vite 构建产物复制到 `src/backend/internal/web/dist/`，由 Go 的 `embed` 机制打包进后端二进制，运行时直接提供 Web UI。
- **模块化隔离**：Agent、Backend、Shared 各自独立 `go.mod`，通过 `go.work` 联合开发；前端独立 `package.json`，通过 `scripts/generate-api-types.mjs` 从 `docs/openapi.yaml` 生成 TypeScript 类型。
- **安装解耦**：`install.sh` 仅负责版本解析与子脚本调度，具体安装逻辑按组件拆分为 `install-panel.sh` / `install-agent.sh`，随 release tag 分发。

## 4. 开发者应遵循的规则
- **新增 Go 模块**：需在 `go.work` 中声明，并在 `src/<module>/go.mod` 中维护依赖；确保 `CGO_ENABLED=0` 可编译。
- **修改前端**：通过 `bun run build` 重新生成静态资源，确保 `src/backend/internal/web/dist/` 同步更新（CI 会自动处理）。
- **发布流程**：打 `vX.Y.Z` 标签触发 release 流水线；禁止手动修改 `install.sh` 中的版本号，应由 CI 注入。
- **环境变量**：本地构建可通过 `VERSION`、`GITHUB_REPO` 控制 ldflags；Docker 运行需设置 `LATTIX_DEPLOY_MODE`、`LATTIX_DB`、`LATTIX_TLS_DIR`、`LATTIX_ACME_CACHE`。
- **E2E 测试**：使用 `scripts/dev-e2e-*.sh` 系列脚本，依赖外部 xray-core，可在 CI 外通过设置 `PANEL_BIN`、`AGENT_BIN`、`XRAY_BIN` 变量运行。