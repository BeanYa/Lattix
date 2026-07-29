---
kind: dependency_management
name: Go 多模块与前端包依赖管理
category: dependency_management
scope:
    - '**'
source_files:
    - go.work
    - go.work.sum
    - src/agent/go.mod
    - src/backend/go.mod
    - src/shared/go.mod
    - src/frontend/package.json
    - src/frontend/bun.lock
    - Dockerfile
---

## 1. 使用的系统与工具
- **Go 工作区（go.work）**：根目录通过 `go.work` 聚合 `src/agent`、`src/backend`、`src/shared` 三个 Go 模块，统一版本与替换规则。
- **Go 模块（go.mod / go.sum）**：每个子模块独立声明依赖，使用 `replace` 将内部 `lattix/shared` 指向本地 `../shared`，实现跨模块共享代码而不发布。
- **Bun（package.json + bun.lock）**：前端使用 Bun 作为包管理器，锁定所有依赖版本于 `bun.lock`，构建时强制 `--frozen-lockfile` 保证可重现。
- **Docker 多阶段构建**：`Dockerfile` 中分别以 `oven/bun:1-alpine` 和 `golang:1.26-alpine` 镜像完成前端构建与后端编译，依赖下载在构建阶段完成。

## 2. 关键文件与位置
- `go.work`、`go.work.sum`：定义 Go 工作区与全局依赖摘要。
- `src/agent/go.mod`、`src/backend/go.mod`、`src/shared/go.mod`：各模块的依赖清单与 `replace` 规则。
- `src/frontend/package.json`、`src/frontend/bun.lock`：前端依赖声明与锁定文件。
- `Dockerfile`：前端依赖安装（`bun install --frozen-lockfile`）与 Go 依赖解析（基于 `go.work`）的统一入口。

## 3. 架构与约定
- **内部共享库**：`src/shared` 作为纯 Go 模块被 agent 与 backend 通过 `require lattix/shared v0.0.0` 引用，并通过 `replace lattix/shared => ../shared` 在本地开发时指向源码，避免发布私有模块。
- **版本策略**：Go 依赖采用精确版本或 commit hash（如 `v1.82.1`、`v0.59.1-0.20260425...`），确保构建可重现；前端依赖使用语义化版本范围（`^`、`~`），由 `bun.lock` 锁定实际解析结果。
- **构建隔离**：Docker 构建中先拷贝 `package.json` 与 `bun.lock` 执行 `bun install --frozen-lockfile`，再拷贝源码，利用 Docker 缓存加速依赖层；Go 侧通过 `go.work` 一次性解析所有模块依赖。
- **无 vendoring**：未使用 `go mod vendor`，依赖直接从代理/网络拉取并写入 `go.sum`；前端依赖通过 `node_modules` 与 `bun.lock` 管理。

## 4. 开发者应遵循的规则
- **添加新依赖**：
  - Go：在对应模块的 `go.mod` 中使用 `go get` 添加，保持 `go.sum` 同步提交。
  - 前端：在 `src/frontend/package.json` 中添加后运行 `bun install` 生成/更新 `bun.lock`，CI 会校验 `--frozen-lockfile`。
- **修改共享库**：直接编辑 `src/shared` 源码，无需发布；确保 `agent` 与 `backend` 的 `replace` 仍指向 `../shared`。
- **版本升级**：优先使用 `go mod tidy` 与 `bun update` 进行依赖更新，审查变更后再提交 `go.sum` 与 `bun.lock`。
- **构建一致性**：本地开发可使用 `go work sync` 与 `bun install` 保持与 CI/Docker 一致；禁止手动修改 `go.sum` 或 `bun.lock` 中的哈希值。
- **私有依赖**：当前未发现 GOPRIVATE/GONOSUMDB 等配置，若引入私有 Go 模块需在工作区层面配置代理与认证。