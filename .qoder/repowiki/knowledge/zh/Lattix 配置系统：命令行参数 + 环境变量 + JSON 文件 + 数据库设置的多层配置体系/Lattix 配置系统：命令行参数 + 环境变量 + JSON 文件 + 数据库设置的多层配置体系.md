---
kind: configuration_system
name: Lattix 配置系统：命令行参数 + 环境变量 + JSON 文件 + 数据库设置的多层配置体系
category: configuration_system
scope:
    - '**'
source_files:
    - src/agent/cmd/agent/main.go
    - src/agent/internal/xray/manager.go
    - src/agent/internal/xray/config.go
    - src/agent/internal/state/state.go
    - src/backend/cmd/backend/main.go
    - src/backend/internal/store/settings.go
    - src/shared/config.go
---

## 1. 使用的系统与框架
- **Go 标准库 `flag`**：所有二进制（agent、backend）通过命令行参数暴露可覆盖的配置项。
- **`os.Getenv` + `envOr` 辅助函数**：后端使用统一的 `envOr(key, fallback)` 从环境变量读取，前缀为 `LATTIX_`。
- **JSON 文件持久化**：Agent 侧使用 `/opt/lattix-agent/data/state.json`、`settings.json`，xray 配置文件为 `/opt/lattix-agent/config/xray.json`。
- **SQLite 数据库 settings 表**：Backend 将面板运行时设置（TLS、管理员密码、告警、Agent 全局设置等）持久化到 SQLite，启动时 DB 值优先于命令行参数。
- **无外部配置框架**：未引入 Viper、pflag、yaml/toml 解析器，全部基于原生实现。

## 2. 核心文件与位置
- **Agent 入口与参数**：`src/agent/cmd/agent/main.go`（-panel/-token/-state/-settings/-xray-bin/-xray-config/-xray-api/-xray-runner/-xray-release-base）
- **Agent xray 管理器**：`src/agent/internal/xray/manager.go`、`src/agent/internal/xray/config.go`（config.json 原子落盘、漂移检测、骨架重建）
- **Agent 本地状态**：`src/agent/internal/state/state.go`（state.json、settings.json 的 Load/Save）
- **Backend 入口与参数**：`src/backend/cmd/backend/main.go`（-addr/-db/-log-dir/-static/-admin-user/-admin-pass/-public-url/-tls-cert/-tls-key/-tls-dir/-tls-acme-domain/-tls-acme-cache/-tls-acme-email）
- **Backend 设置存储**：`src/backend/internal/store/settings.go`（DB 键常量 Setting* 与 Get/Set/Delete API）
- **共享协议与模板占位符**：`src/shared/config.go`（VirtualConfig、RealizedConfig、{{PORT}}/{{PRIVATE_KEY}}/{{CLIENTS}} 等占位符）

## 3. 架构与设计决策
### 3.1 配置分层与优先级
| 层级 | 来源 | 说明 |
|------|------|------|
| L1 运行时参数 | `-flag` 命令行 | 最高优先级，进程启动即生效 |
| L2 环境变量 | `LATTIX_*` / `LATX_*` | 覆盖 flag 默认值，便于容器/CI 注入 |
| L3 数据库设置 | SQLite `settings` 表 | Backend 特有；DB 有值则覆盖 L1/L2，重启生效 |
| L4 JSON 文件 | state.json / settings.json / xray.json | Agent 本地持久化，跨重启保留 |

### 3.2 Agent 侧配置流
- **首次连接**：通过 `-token` 或 state.json 中的长期凭证建立 WS 会话。
- **设置同步**：Panel 下发 `AgentSettings`，Agent 写入 `settings.json`，热更新遥测/漂移检测间隔。
- **xray 配置管理**：Agent 独占管理 `xray.json`，采用“模板填充 → `xray run -test` 校验 → 原子 rename 落盘 → gRPC 热操作 → 失败回滚”流水线。
- **漂移检测**：对 config.json 做 SHA256 哈希比对，外部修改触发净化重建（仅保留 `node_*` inbound 与链 piece）。

### 3.3 Backend 侧配置流
- **TLS 模式**：`off/cert/acme/path` 四种模式，DB 中 `tls_mode` 优先于启动参数；path/acme 支持证书热加载无需重启。
- **管理员密码**：bcrypt 哈希存 DB，`-reset-admin` 命令行可在线重置并失效所有会话。
- **日志与配额**：operation_log_limit、request_log_max_mb 等通过 `GetSetting` 动态读取。

### 3.4 共享配置模型（shared/config.go）
- **VirtualConfig**：面板侧虚拟配置（protocol/port/flow/network/template），含 `{{PORT}}`、`{{PRIVATE_KEY}}`、`{{CLIENTS}}`、`{{DECRYPTION}}`、`{{TAG}}` 占位符。
- **RealizedConfig**：Agent 上报的实际生效值（端口、公钥、指纹等），用于订阅生成。

## 4. 开发者应遵循的规则
1. **新增配置项**：在对应 main.go 中添加 `-flag`，并通过 `envOr("LATTIX_X", default)" 提供环境变量覆盖。
2. **持久化设置**：Backend 新增设置需在 `store/settings.go` 声明 `Setting*` 常量，并提供 Get/Set/Delete 语义。
3. **Agent 设置文档**：通过 `shared.AgentSettingsDocument` 结构体定义，带 `Validate()` 校验，由 Panel 下发、Agent 落盘 `settings.json`。
4. **xray 配置变更**：必须走 `Manager.ApplyNode/RemoveUser` 等受控路径，禁止直接写 `xray.json`；确保 `commitConfig` 的原子性与回滚机制。
5. **敏感信息**：token、私钥、密码哈希一律不落明文日志；state.json 权限 0600。
6. **迁移兼容**：DB 设置删除后应回退到命令行默认值（`DeleteSetting` 语义）。
7. **测试约定**：e2e 脚本通过环境变量注入镜像地址、release base 等，避免硬编码。