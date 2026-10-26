# Node.js 2026-10-21 安全公告对 Lattix 的影响评估

**评估日期**：2026-10-22 | **评估人**：安全架构师 | **版本**：1.0

**结论一句话**：Lattix 生产运行时（Go 静态二进制 + alpine:3.22）**不含 Node.js，零暴露**；本公告不需要任何生产升级。真正需要动作的是**开发机 Node**、**CI 的 Node 版本漂移**，以及评估中发现的更值得优先处理的两个相邻问题（**镜像内 Go 工具链落后 4 个补丁**、**Bun 构建容器未修复的 Brotli 解码器**）。

---

## 1. 公告核对（逐条比对官方页面）

来源：<https://nodejs.org/en/blog/vulnerability/october-2026-security-releases>（Last Updated 2026-10-21）

**最高严重级别：HIGH（无 CRITICAL）。** 修复版本：**v26.12.1 / v24.22.1 / v22.23.4**（已通过 nodejs.org/dist/index.json 核对发布日期：2026-10-21 / 2026-10-20 / 2026-10-20）。

| # | CVE | 描述 | 评级 | 26.x | 24.x | 22.x |
|---|-----|------|------|:----:|:----:|:----:|
| 1 | CVE-2026-105482 | dns.resolve 截断含 NUL 字符的域名 | high | Y | Y | Y |
| 2 | CVE-2026-105483 | IP 以全角数字书写时 TLS 主机名校验绕过 | high | Y | Y | - |
| 3 | CVE-2026-106069 | 主机名"松散解析"为 IP 时 TLS 主机名校验绕过 | high | Y | Y | Y |
| 4 | CVE-2026-111497 | **Brotli 解码器越界写，可致 RCE** | high | Y | Y | Y |
| 5 | CVE-2026-105481 | keep-alive 套接字处理"请求发出前写入的响应" | medium | Y | Y | Y |
| 6 | CVE-2026-106072 | 非规范 IPv4 主机名校验缺陷，DNS SAN 可认证 IP 端点 | medium | Y | Y | Y |
| 7 | CVE-2026-106074 | randomUUID/randomInt 熵缓存被写入 SEA/启动快照，进程间重复 | medium | Y | Y | Y |
| 8 | CVE-2026-106070 | ZipFile 在 ZIP 头校验失败后返回攻击者可控前缀字节 | low | Y | - | - |
| 9 | CVE-2026-106071 | Permission Model 通配符基数查找过度授权 | low | Y | - | - |
| 10 | CVE-2026-106073 | Permission Model 经 fs.realpathSync 绕过 | low | Y | - | - |

**按发布线计数**：26.x 受 10 个、24.x 受 8 个、22.x 受 7 个影响。

**同批次的依赖更新**：c-ares（26/24/22）、ca-cert（eMudhra 中间 CA 失陷）、OpenSSL（24/22）、SQLite（22）、Undici（26/24/22）。这些都是 Node **内置**依赖，不通过 npm 分发 —— 所以"升级 npm 包"修不了它们，只能升级 Node 本体，或者什么都不做（如本项目）。

> Node 20（Iron）已于 2026-04 结束 LTS 且本次无修复版本 —— 若任何环境仍是 20.x，属于"永久受影响"，必须跨大版本升级。

---

## 2. 受影响面判定

### 2.1 判定矩阵

| 面 | 是否有 Node.js | 结论 | 证据 |
|---|---|---|---|
| **生产运行时** | 否 | **零暴露** | 最终镜像仅 `/app/lattix-backend` + alpine 基础层；`go version -m` 显示为 Go 静态二进制 |
| **生产容器镜像** | 否 | **零暴露** | 拉取 `ghcr.io/beanya/lattix:latest`（构建于 2026-10-22T00:16Z，4 层）逐层检查：无 node、无 bun、无 node_modules |
| **前端构建阶段（Docker）** | 否（是 Bun） | **不受这些 CVE 影响** | `oven/bun:1.3.14-alpine` 内 `/usr/local/bun-node-fallback-bin/node` 是**指向 `/usr/local/bin/bun` 的符号链接**，且该路径在镜像 PATH 中 |
| **CI** | 是（runner 预装） | **受影响版本，但无可达攻击面** | 未用 setup-node，`bun run build` 里的 `node` 落到 runner 预装的 **Node.js 22.23.3**，< 22.23.4 |
| **开发机** | 是 | **受影响** | 本机 `node v24.21.0`（Homebrew node@24 24.21.0_2）< 24.22.1 |
| **构建脚本** | 是（运行于 Node/Bun） | **不受影响（不可达）** | 仅用 node:fs/promises、node:path、node:url、node:zlib.gzipSync；不涉及 dns/TLS/Brotli/Permission Model/SEA |

### 2.2 关键取证：Docker 构建阶段实际跑的是 Bun，不是 Node

```
usr/local/bun-node-fallback-bin/node -> /usr/local/bin/bun     # 镜像内符号链接
PATH=...:/usr/local/bun-node-fallback-bin                       # 镜像 Env
```

因此 Dockerfile 第 9 行 `RUN bun run build` 展开的 `node scripts/generate-api-types.mjs --check` 由 **Bun 执行**（同理 tsc / vite 的 `#!/usr/bin/env node` shebang 也落到 Bun）。语义等价，但运行时不同 —— 这也说明这些 Node CVE 在镜像构建里不成立。

### 2.3 为什么"受影响"也不等于"可利用"

10 个 CVE 的可达性都需要特定 API 被喂入不可信输入：dns.resolve、TLS 客户端校验、Brotli 解压、HTTP keep-alive 客户端、--permission 模式、SEA/快照、node:sqlite。Lattix 侧：

- 构建脚本是**纯本地文件读写 + gzip**，无网络、不解压不可信数据；
- 前端构建输入全部来自仓库自身（含 docs/openapi.yaml），不来自用户；
- 生产 HTTP 服务是 Go，不是 Node。

结论：**即便在 CI/开发机上，这 10 个 CVE 也没有一条实际可达路径**；升级属于清债，不是救火。

---

## 3. 镜像基础层是否受影响（逐层核对）

已发布镜像 `ghcr.io/beanya/lattix:latest`（amd64 manifest sha256:b8a7a25587f986bc1d87c758e80a0342fafffbbbf340f5df18ba4ac21292c34e，created 2026-10-22T00:16:25Z）：

| 组件 | 版本 | 说明 |
|---|---|---|
| Alpine | **3.22.7** | 基础层 |
| musl | 1.2.5-r13 | |
| busybox | 1.37.0-r20 | |
| **ca-certificates(-bundle)** | **20261020-r0** | 由 NSS_3_131_1_RTM 生成，**已含 eMudhra 移除** |
| tzdata | 2026e-r0 | |
| Go 工具链（构建期） | **go1.26.6** | 见 5.1，本次评估中最值得修的一项 |

**CA 信任库专项**（本公告的 ca-cert 条目与 2026-10 eMudhra 中间 CA 失陷是同一件事）：

- Mozilla 在 NSS 3.131.1 移除了用于签发这些中间 CA 的根证书；比对 certdata 实测：移除的是 **emSign ECC Root CA - G3 / emSign Root CA - C1 / emSign ECC Root CA - C3**，**保留 emSign Root CA - G1**（Mozilla 判断 G1 未受影响）。
- Alpine 3.22 的 `ca-certificates 20261020-r0` 正是该 certdata，已发布镜像里也是它 —— 解码 118 张根证书后确认：**仅剩 emSign Root CA - G1，三个被移除的根均已不存在**。
- 对 Lattix 的意义：Go 后端在 Linux 上用系统根证书池（/etc/ssl/certs/ca-certificates.crt），ACME（autocert）、外部订阅拉取、Webhook、CDN 目录、自更新全部走这套信任。**当前镜像已符合 Mozilla 的修复建议，无需额外动作。**
- 长期注意：Go/OpenSSL 没有 OneCRL/CRLSet 这种"按 SPKI 精确吊销中间 CA"的机制，只能整根移除。下次再出现"只吊销中间 CA"的事件，Go 侧不会自动获得保护（见 6 的 P3）。

---

## 4. 是否需要升级 Node / 依赖

**生产：不需要。** 没有 Node 运行时，也不存在"为修这些 CVE 而升级依赖"的路径 —— CVE 位于 Node 内置组件（c-ares/OpenSSL/SQLite/Undici），npm 层无法修复。

**开发机：需要。** 见 6 的 P2。

**依赖层：无高危待办。** CI 的 `bun audit --audit-level high` 在最近一次运行（2026-10-22T00:10:20Z）通过；bun.lock 中无 undici 包（只有类型包 undici-types），无 node:sqlite 使用。

---

## 5. 评估中发现的、优先级高于本公告的问题

### 5.1 【P1】生产镜像的 Go 工具链落后 4 个补丁版本

```
$ go version -m lattix-backend
/app/lattix-backend: go1.26.6
```

- Dockerfile 用 `golang:1.26.6-alpine` 构建（2026-08-13），而 CI 用 **1.26.10**；go.mod/go.work 虽写了 `toolchain go1.26.10`，实测**没有生效**（官方 golang 镜像 GOTOOLCHAIN 行为），产物仍是 1.26.6。
- go1.26.9（2026-10-08）修了 **crypto/tls、net/http、net/textproto、html/template、os** 的安全问题；go1.26.10（2026-10-19）修了 **crypto/x509**。
- 后果：**GHCR 镜像里的二进制缺少这些修复，而 release tarball 里的二进制（runner 构建）是 1.26.10** —— 两条发布路径产物不一致；`govulncheck` 在 CI 按 1.26.10 的 stdlib 判定，**看不到这个缺口**。
- 对一个以 TLS 为核心（ACME、mTLS、代理链路）的产品，crypto/x509、crypto/tls 的补丁不是可选项。

### 5.2 【P2】Bun 构建容器共享未修复的 Brotli 解码器

- Bun 官方 issue #46352（2026-10-21 建、2026-10-22 关）：**所有已发布 Bun 版本**（<=1.4.2 用 brotli 1.1.0，1.4.3 用 1.2.0）都缺少 CVE-2026-111497 的上游修复；维护者回复"今天会发带修复的版本"，但**截至核对时最新发布仍是 1.4.3**（2026-10-10）。
- 该解码器默认经 fetch() 的 `Content-Encoding: br` 响应、node:zlib、DecompressionStream("brotli") 触达。
- 对 Lattix：构建阶段只跑 bun install + bun run build，脚本不解压 Brotli 数据，**当前无可达路径**；但这属于"构建容器处理远端数据"的固有面（registry/CDN 若协商 br）。

### 5.3 【P2】CI 的 Node 版本是隐式的、且随 runner 镜像漂移

同一份 `bun run build` 在三种环境中由**三个不同运行时**执行：Docker=Bun、CI=runner Node 22.23.3、开发机=Node 24.21.0。构建不可复现，也让"我们到底受哪些运行时 CVE 影响"无法静态回答。

### 5.4 【P3】信任库可观测性

现在没有任何地方记录/告警运行时信任库版本。建议在发布流水线打印 `apk info -v ca-certificates`，并在下次"仅吊销中间 CA"类事件中手工评估 Go 侧影响（Go 无 OneCRL）。

---

## 6. 行动清单与优先级

| 优先级 | 动作 | 位置 | 说明 |
|---|---|---|---|
| **P1（本周）** | `golang:1.26.6-alpine` -> `golang:1.26.10-alpine` | Dockerfile:11 | 直接修复 5.1；顺带消除"测 1.26.10 / 发 1.26.6"的不一致 |
| **P1（本周）** | 发布流水线加镜像级扫描（Trivy/Grype，读 buildinfo 覆盖 Go stdlib） | .github/workflows/release.yml | 让"工具链落后"自动暴露，而非靠人工比对 |
| **P2（本周）** | 开发机 Node 24.21.0 -> **>=24.22.1** | 本机 | Homebrew 当前 node@24=24.22.0、node=26.12.0，**都还差一个补丁**，brew upgrade 不足以修复；用官方 tarball/pkg 或 nvm 装 24.22.1 |
| **P2（本周）** | CI 显式钉 Node：setup-node + node-version 24.22.1，并加 .node-version | ci.yml / release.yml | 消除 5.3；把 runner 的 22.23.3 从构建链移除 |
| **P2（跟踪）** | Bun 1.3.14 -> >=1.4.4（待 brotli 修复发布） | Dockerfile:3、ci.yml:25 | 1.3.14 是 2026-05 的版本，已落后 4 个 minor；升级前确认修复已进发布 |
| **P3（本迭代）** | `@types/node` ^24.13.2 -> ^24.22.x（lock 内为 24.13.3） | src/frontend/package.json:42 | 类型对齐运行时 |
| **P3（本迭代）** | package.json 加 `"engines": { "node": ">=24.22.1" }` | src/frontend/package.json | 让 IDE/CI 有据可依（Bun 忽略，无副作用） |
| **P3（持续）** | 基础镜像 digest 固定 + Dependabot/Renovate 管 Docker | Dockerfile:3,11,24 | 目前只有 tag 发布才重建镜像，补丁级更新靠人工 |

### 不需要做的事（避免安全表演）

- 不要为了本公告在 Docker 构建阶段安装 Node —— 构建用它毫无收益，只会新增一个需要打补丁的运行时。
- 不要因此改生产运行时或数据库层 —— 与本公告无关。
- 不要盲目 npm audit fix 全量升依赖 —— 与这些 CVE 无关，反而引入回归风险。

---

## 7. 建议的补丁

```diff
--- a/Dockerfile
+++ b/Dockerfile
@@ -8,7 +8,9 @@
 COPY docs/openapi.yaml /docs/openapi.yaml
+# 注意：本镜像内 node 是指向 bun 的兼容符号链接（/usr/local/bun-node-fallback-bin/node），
+# 因此 build 脚本实际由 Bun 执行；这是有意为之，非疏忽。
 RUN bun run build
 
-FROM golang:1.26.6-alpine AS backend
+FROM golang:1.26.10-alpine AS backend
```

```diff
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -21,6 +21,10 @@
       - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
+      # 显式钉住构建脚本使用的 Node —— 不再依赖 runner 镜像默认版本
+      - uses: actions/setup-node@v4
+        with:
+          node-version: 24.22.1
       - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2
```

> 若希望彻底统一为单一运行时，可把 package.json 里的 `node scripts/...` 改成 `bun scripts/...`：Docker 与 CI 行为一致、不需要 Node。代价是本地开发机必须装 Bun（当前本机未安装）。二者择一即可，**不要停留在"隐式三运行时"的现状**。

---

## 8. 复现我做的检查

```bash
# 1) 公告与修复版本
curl -s https://nodejs.org/dist/index.json | python3 -c 'import sys,json;print([r["version"] for r in json.load(sys.stdin) if r["version"] in ("v24.22.1","v22.23.4","v26.12.1")])'

# 2) 生产镜像里有没有 Node？信任库是什么版本？
docker run --rm --entrypoint sh ghcr.io/beanya/lattix:latest -c \
  'command -v node bun || echo "no node/bun"; apk info -v ca-certificates; cat /etc/alpine-release'

# 3) 镜像内二进制的 Go 版本（本报告 5.1 的证据）
docker run --rm --entrypoint sh ghcr.io/beanya/lattix:latest -c 'cat /app/lattix-backend > /tmp/b'  # 或解层后：
go version -m ./app/lattix-backend   # -> go1.26.6

# 4) 构建阶段是不是 Bun？
docker run --rm --entrypoint sh oven/bun:1.3.14-alpine -c 'ls -l /usr/local/bun-node-fallback-bin/node; command -v node'

# 5) CI 实际用的 Node（建议在 ci.yml 加 run: node -v 永久可见；当前推断 runner 默认 22.23.3）
```

---

## 9. 残余风险与待确认项

1. **CI 的 Node 版本**：由 runner-images 清单推断为 22.23.3；建议按 6 补 setup-node 后以流水线实测为准。
2. **Bun 修复版本号**：维护者承诺 2026-10-21 发版，但截至 2026-10-22 03:00Z 尚未发布；升级前需确认 release notes 明确包含 brotli 补丁。
3. **eMudhra 事件后续**：目前 Mozilla 只移除 G3/C1/C3 三张根、保留 G1；若 CA/B 论坛扩大范围，需重新评估第 3 节的信任库。
4. **本报告未覆盖**：Go 业务代码本身的授权/注入类问题（govulncheck 在 CI 通过），以及前端依赖的供应链风险（bun audit 通过）。
