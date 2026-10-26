# Lattix 代码级审计：Node.js 2026-10-21 安全公告可达性

**审计日期**：2026-10-22 | **审计人**：AI 生成代码安全审计师（subagent） | **状态**：v1.0（完成）
**范围**：`src/frontend`（React/Vite，Bun 构建；`scripts/*.mjs` 由 node 执行）、`src/backend` 与 `src/agent`（Go）、`Dockerfile`、`.github/workflows/*.yml`。
**约束**：只读，未修改任何业务代码。所有结论以 `文件:行号` 或命令输出为证据；外部事实以官方页面/仓库为准并注明抓取时间。
**前置材料**：`docs/security/2026-10-21-nodejs-advisory-assessment.md`（下称"架构师报告"）。该报告已完成运行时与镜像层判定，本文**引用而不重复**其镜像层取证，重心放在**代码级可达路径**与**按 CWE 的修补清单**。

---

## 0. 结论与建议动作

### 0.1 结论一句话

**10 个 CVE 在 Lattix 的四个 Node 执行面（生产、Docker 构建、CI、开发机）均无可达路径。** 这不是"版本已修"，而是**触发 API 在本仓库的一手 Node 代码中不存在**：四个构建脚本对 Node 内置模块的 import 只有 `node:fs/promises` / `node:path` / `node:url` / `node:zlib.gzipSync`，不涉及 `dns` / `tls` / `http(s)` / `brotli` / Permission Model / SEA / `node:zip`。

真正需要动作的是两类问题：

1. **构建链版本漂移**（CWE-1395）：CI 用 runner 自带 Node **22.23.3**（< 22.23.4）、release 用**未钉版本的 Bun**（默认 latest）、开发机 Node **24.21.0**（< 24.22.1）。版本层面确实暴露，但仓库内路径不可达；修它是清债 + 可复现性，不是救火。
2. **Go 侧同类 CWE 的一个真实可达缺陷**（CWE-367/CWE-918）：`GuardedDialContext` 先解析、后按主机名拨号，存在 **DNS rebinding TOCTOU**，可绕过 SSRF 防护访问内网。这是本次审计唯一"真实可达"的代码级问题，建议 P1。

### 0.2 建议动作（按优先级）

| 优先级 | 动作 | 位置 | 对应 CWE | 说明 |
|---|---|---|---|---|
| **P1（本周）** | 修复 SSRF 拨号防护的 DNS rebinding TOCTOU：解析一次后直接拨 IP，并保留 TLS ServerName | `src/shared/requester/guarded_dial.go:61-71,84-109` | CWE-367 / CWE-918 | 唯一真实可达项；见 §4.1/§4.4 |
| **P1（本周）** | CI 显式钉 Node **24.22.1**，并加 `.node-version` + `engines` | `.github/workflows/ci.yml:21-31`、`src/frontend/package.json:8-12` | CWE-1395 | 当前 runner Node 22.23.3 受 7 个 CVE；runner 迁到 26.04 后会变成 24.21.0（仍受 8 个） |
| **P1（本周）** | release 钉 Bun 版本，消除"CI 1.3.14 / release latest / Docker 1.3.14"三方漂移 | `.github/workflows/release.yml:34-40` | CWE-1395 | `setup-bun` 未写 `bun-version` 时默认取 package.json 或 latest |
| **P2（本迭代）** | Bun 1.3.14 → 含 Brotli 修复的版本（截至 2026-10-22T03:20Z 最新仍为 1.4.3，修复未发版） | `Dockerfile:3`、`.github/workflows/ci.yml:25` | CWE-1395 | 构建阶段不解 Brotli，当前不可达；属构建容器固有面 |
| **P2（本迭代）** | 开发机 Node 24.21.0 → ≥24.22.1；`engines`/`.node-version` 落地 | 本机 / `src/frontend/package.json` | CWE-1395 | 版本暴露，路径不可达；避免开发者本机长期落后 |
| **P2（本迭代）** | 外部订阅 `skip_cert_verify` 增加风险提示、审计日志与最小权限约束 | `src/backend/cmd/backend/main.go:471-477`、`src/backend/internal/extsub/service.go:153-157` | CWE-295 | 管理员显式开关，属风险接受；缺的是可观测与误用防护 |
| **P3（跟踪）** | 自更新解压增加解压后大小上限（zip/tar bomb） | `src/agent/internal/xray/upgrade.go:167-190`、`src/backend/internal/panel/selfupdate/update.go:583-628` | CWE-409 | 输入是已校验的 release 资产，需镜像/发布链先被攻破 |
| **P3（跟踪）** | 自更新增加签名校验（checksums.txt 与二进制同源，TLS 之外无签名） | `src/agent/internal/selfupdate/selfupdate.go:93-99`、`src/backend/internal/panel/selfupdate/update.go:377-407` | CWE-494 | 供应链加固；团队已在注释中标记为 P2 供应链项 |
| **P3（跟踪）** | `@types/node` ^24.13.2（lock 24.13.3）→ ^24.22.x | `src/frontend/package.json:42` | —（类型层） | 无安全影响，只影响类型覆盖 |
| **交叉引用** | Go 工具链 1.26.6 → 1.26.10 | `Dockerfile:11` | CWE-1395 | 架构师报告 §5.1 已列为 P1，本文不重复评级；合并排期时应高于本文 P1-2 |

### 0.3 对前置报告的两处校正（不影响结论）

1. **CVE-2026-106073 的受影响版本**：架构师报告 §1 表格（`docs/security/2026-10-21-nodejs-advisory-assessment.md:26`）把 24.x/22.x 标为 `-`，但官方页面明确写 "This vulnerability affects Node.js 26.x, 24.x, and 22.x."。因此 24.21.0 受 **8** 个 CVE（不是 7 个），22.23.3 受 **7** 个。报告正文的计数 "10/8/7" 反而与官方一致，只是表格单元格标错。
2. **官方计数复核**：官方页面逐条核对结果为 26.x=10、24.x=8、22.x=7；与架构师报告 §1 末段一致。本文 §3.1 使用修正后的矩阵。

> 说明：上述校正不改变"生产无 Node、构建脚本不触达"的结论；它只影响版本暴露面的计数，以及未来若启用 Permission Model 时的复核范围。

---

## 1. 审计口径

### 1.1 "可达"的三要素

本文把"真实可达"定义为以下三者同时成立：

1. **存在一个执行 Node 的进程**（生产/构建/CI/开发机/工具脚本）；
2. **该进程调用了 CVE 对应的触发 API**（如 `dns.resolve`、TLS 客户端、Brotli 解码器、`--permission`）；
3. **该 API 接收不可信输入**（攻击者能控制主机名、响应体、压缩数据、权限参数等）。

缺任一条即判"不可达"。若版本暴露但缺 2/3，标为"理论风险/版本暴露"，不夸大为漏洞。

### 1.2 证据标准

- 代码位置一律给 `文件:行号`；命令输出给可复现命令（§5）。
- 外部事实给官方 URL 与抓取时间；版本号以 `nodejs.org/dist/index.json`、GitHub runner-images README、Bun releases API 为准。
- 区分"已验证"与"推断"：CI 的 Node 版本来自 runner 镜像 README（已验证镜像内容），但具体 workflow 运行日志未在本机留存；已在 §6 标注。

### 1.3 不在本文范围

- 业务授权/越权、SQL 注入、前端 XSS、CSRF 等应用层漏洞（本次只围绕公告与同类 CWE 的代码路径）。
- 生产镜像逐层扫描、CA 信任库核对（架构师报告 §3 已完成）。
- npm/Bun 依赖的完整供应链审计（架构师报告 §4 记录 `bun audit --audit-level high` 通过；本机未安装 Bun，未复跑；本文只核查与 CVE 触发 API 相关的依赖）。

---

## 2. Node 执行面盘点：本仓库到底哪里会跑 Node

| # | 执行面 | 运行时 | 版本来源 | 证据 | 输入可信度 |
|---|---|---|---|---|---|
| 1 | 生产容器 | **无 Node** | — | `Dockerfile:24-42`（最终层仅 alpine + Go 二进制）；架构师报告 §2.1/§3 已做镜像层取证 | — |
| 2 | Docker 前端构建 | **Bun**（`node` 是 bun 的兼容符号链接） | `oven/bun:1.3.14-alpine` | `Dockerfile:3,9`；架构师报告 §2.2 | 仓库自身 |
| 3 | CI `frontend` job | Bun 1.3.14 + **runner 系统 Node 22.23.3** | `ci.yml:23-25` 只钉 Bun，无 `setup-node` | `.github/workflows/ci.yml:16-31`；runner-images Ubuntu 24.04 README `- Node.js 22.23.3` | 仓库自身 |
| 4 | CI `release` job 前端构建 | **未钉版本的 Bun（默认 latest）** + runner 系统 Node | `release.yml:34` 无 `bun-version` | `.github/workflows/release.yml:34-40`；setup-bun README 默认 `Version from package.json, or latest` | 仓库自身 |
| 5 | 开发机 | **Node v24.21.0**（本机实测）；本机未安装 Bun | Homebrew | `node -v` → `v24.21.0`；`bun -v` → `command not found` | 仓库自身 + 开发者 |
| 6 | 构建脚本 | 由 #2/#3/#4/#5 的 `node` 执行 | `package.json:8-12` 字面写 `node scripts/...` | `src/frontend/package.json:8-12` | 仓库自身 |
| 7 | Agent 技能工具（非产品） | Node（手工运行） | `.agents/skills/brainstorming/scripts/server.cjs` | 使用 `node:http`/`crypto` 的本地 WebSocket 服务；不在构建/CI/生产链路 | 本地 agent 通信 |

**关键事实**：

- `package.json` 的 build 脚本是 `node scripts/generate-api-types.mjs --check && tsc -b && vite build && node scripts/check-build-chunks.mjs`（`src/frontend/package.json:8`）。`bun run build` 只是外层执行器，**不会把 package.json 脚本里的 `node` 改写为 `bun`**；因此 CI/release 的脚本步骤确实由 runner 的系统 Node 执行。
- Docker 内 `node` 指向 Bun 的符号链接（架构师报告 §2.2），所以同一条命令在 Docker 内由 Bun 执行；这是"同一份构建脚本、三种运行时"的根源。
- 仓库没有根 `package.json`，没有 `.node-version` / `.nvmrc`，`src/frontend/package.json` 也没有 `engines` 或 `packageManager` 字段（已用 `ls`/`grep` 验证，§5）。

### 2.1 版本矩阵（本次审计的核心事实）

| 组件 | Docker 构建 | CI | Release | 开发机 |
|---|---|---|---|---|
| Bun | 1.3.14 | 1.3.14 | **latest（漂移）** | 未安装 |
| Node | 无（bun 链接） | **22.23.3** | **22.23.3**（同一 runner 镜像） | **24.21.0** |
| Go | **1.26.6** | 1.26.10 | 1.26.10 | 未核对 |

> 结论：**构建不可复现，且"我们到底受哪些运行时 CVE 影响"无法静态回答。** 这不是本公告引入的漏洞，但它让本公告的暴露面长期不可判定。

---

## 3. 逐 CVE 可达性判定（10 项）

### 3.1 公告事实（官方页面逐条核对）

来源：<https://nodejs.org/en/blog/vulnerability/october-2026-security-releases>（Last Updated 2026-10-21；本文 2026-10-22 抓取）。修复版本 **26.12.1 / 24.22.1 / 22.23.4**（`nodejs.org/dist/index.json` 核对发布日期：2026-10-21 / 2026-10-20 / 2026-10-20）。最高严重级别 **HIGH**，无 CRITICAL。

| # | CVE | 严重度 | 26.x | 24.x | 22.x | 触发 API（公告语义） |
|---|---|:---:|:---:|:---:|:---:|---|
| 1 | CVE-2026-105482 | high | Y | Y | Y | `dns.resolve*`（c-ares）处理含 NUL 的域名 |
| 2 | CVE-2026-105483 | high | Y | Y | - | TLS 客户端主机名校验（IP 写成全角数字） |
| 3 | CVE-2026-106069 | high | Y | Y | Y | TLS 客户端主机名校验（主机名"松散解析"为 IP） |
| 4 | CVE-2026-111497 | high | Y | Y | Y | Brotli **解码器**越界写（可致 RCE） |
| 5 | CVE-2026-105481 | medium | Y | Y | Y | Node HTTP 客户端 keep-alive 套接字处理"请求前写入的响应" |
| 6 | CVE-2026-106072 | medium | Y | Y | Y | 非规范 IPv4 主机名的证书校验（DNS SAN 可认证 IP 端点） |
| 7 | CVE-2026-106074 | medium | Y | Y | Y | `crypto.randomUUID()`/`randomInt()` 熵缓存写入 SEA/启动快照 |
| 8 | CVE-2026-106070 | low | Y | - | - | Node `ZipFile` 在 ZIP 头校验失败后返回攻击者可控前缀字节 |
| 9 | CVE-2026-106071 | low | Y | - | - | Permission Model 通配符基数查找过度授权 |
| 10 | CVE-2026-106073 | low | Y | Y | Y | Permission Model 经 `fs.realpathSync` 绕过 |

**受影响计数**：26.x=10、24.x=8、22.x=7。本仓库涉及的版本：CI/release Node **22.23.3**（受 7 个）、开发机 Node **24.21.0**（受 8 个）。二者都 < 修复版本。

### 3.2 判定矩阵（10 项 × 本仓库）

| # | CVE | 本仓库触发 API 出现次数 | 有 Node 进程？ | 有不可信输入？ | 判定 | 关键证据 |
|---|---|:---:|:---:|:---:|---|---|
| 1 | 105482 | **0**（无 `node:dns`/`dns.resolve*`） | 是（CI/开发机） | 否 | **不可达** | `grep -rn "dns\." src/frontend/scripts src/frontend/src` 无输出 |
| 2 | 105483 | **0** 一手代码；Vite dev proxy 仅在 `VITE_API_PROXY_TARGET=https://…` 时是 TLS 客户端 | 是（dev server） | 否（目标由开发者设） | **不可达** | `src/frontend/vite.config.ts:6,28-48`；无 `https.request`/`tls.connect` |
| 3 | 106069 | 同上 | 是 | 否 | **不可达** | 同上 |
| 4 | 111497 | **0**（无 `brotliDecompress*`/`DecompressionStream("brotli")`/`fetch` br） | 是 | 否 | **不可达** | 唯一 zlib 用法是 `gzipSync`（编码器）：`check-bundle-budget.mjs:12,49` |
| 5 | 105481 | **0** 一手代码；Vite proxy 的客户端目标是 localhost 后端 | 是 | 否（响应源是自家后端） | **不可达** | `vite.config.ts:32-47`；构建脚本无 `http.request` |
| 6 | 106072 | **0** 一手 TLS 客户端 | 是 | 否 | **不可达** | 同 #2/#3 |
| 7 | 106074 | **0** Node `randomUUID`/`randomInt`；浏览器用 `crypto.getRandomValues` | 是 | 否 | **不可达** | `src/frontend/src/lib/requester.ts:71`、`src/frontend/src/pages/chains/use-chain-form.ts:115`（浏览器 API） |
| 8 | 106070 | **0**（无 `node:zip`/`ZipFile`；shell `unzip` 是独立二进制） | 否（仅 26.x，且本仓库无 26.x 运行时） | 否 | **不可达** | `grep -rn "node:zip" / "ZipFile"` 仅命中前置报告与 e2e 的 Python `zipfile` |
| 9 | 106071 | **0**（无 `--permission`/`--allow-fs-*`） | 是 | 否 | **不可达** | `grep -rn -e "--permission" -e "--allow-fs"` 无输出 |
| 10 | 106073 | **0**（同上） | 是 | 否 | **不可达** | 同上 |

### 3.3 六个需要展开的判定

#### 3.3.1 CVE-2026-105482（dns.resolve + NUL）

- 触发 API 是 `dns.resolve*`（c-ares 路径），不是 `dns.lookup`/`getaddrinfo`。
- 本仓库一手 Node 代码无 `node:dns`，也无任何 `resolve4/resolve6/resolveAny`（§5 命令输出为空）。
- 构建工具链（vite/typescript/@vitejs/@tailwindcss/openapi-typescript/@redocly）扫描 `node:dns`/`require('dns')` 也为 0（§5）。
- 即使 Node 的 `fetch`/undici 在别处被使用，它走的是 `dns.lookup`，与本 CVE 的 c-ares 路径不同；本仓库也没有 `undici` 包（`bun.lock` 只有 `undici-types`）。
- **判定：不可达。** 不是"输入可信所以安全"，而是**该 API 根本不会被调用**。

#### 3.3.2 CVE-2026-105483 / 106069 / 106072（TLS 主机名校验）

三者的共同前提：Node 作为 **TLS 客户端**，连接一个**攻击者可控主机名**的服务器，且证书链/主机名校验被畸形输入绕过。

本仓库的 Node TLS 客户端只有一处可能：

- Vite dev server 的 `/api`、`/sub/` 代理（`src/frontend/vite.config.ts:28-48`）。当 `VITE_API_PROXY_TARGET` 以 `https://` 开头时，`http-proxy` 会走 Node 的 `https.request`，成为 TLS 客户端。
- 但该目标由**开发者本机环境变量**决定（`vite.config.ts:6`），不是攻击者输入；生产构建（`vite build`）不启动代理，不发起网络请求。
- 一手代码没有 `https.request`、`tls.connect`、`undici`、自定义 `Agent`（§5）。
- **判定：不可达。** 唯一理论路径要求开发者把 dev proxy 指向一个恶意 HTTPS 源，且该源能提供畸形证书；这属于开发者本机环境被入侵，不是本仓库的攻击面。

#### 3.3.3 CVE-2026-111497（Brotli 解码器 OOB write）

- 这是**解码器**漏洞；`node:zlib` 的 `brotliDecompressSync`/`brotliDecompress`、`DecompressionStream("brotli")`、以及 `fetch` 对 `Content-Encoding: br` 的自动解压会触达。
- 本仓库唯一的 zlib 用法是 `gzipSync`（**编码**，不是解码）：`src/frontend/scripts/check-bundle-budget.mjs:12,49`。gzip 与 brotli 是不同算法/不同代码路径。
- 一手代码无 `fetch`（浏览器端的 `fetch` 在 `src/frontend/src/lib/requester.ts:134,194,262,328`，运行在浏览器，不是 Node；Vitest 中 `fetch` 全部被 `vi.stubGlobal('fetch', …)` 替换，见 `src/frontend/src/lib/requester.test.ts:35` 等，无真实网络）。
- 构建工具链扫描 `brotliDecompress`/`DecompressionStream` 为 0（§5）。
- Go 侧的 CDN catalog 客户端还显式要求 `Accept-Encoding: identity` 并拒绝非 identity 响应（`src/backend/internal/cdncatalog/catalog.go:160,171-173`），这是同一类"不解压不可信数据"的正确控制。
- **判定：不可达。** 注意：Docker/CI 的 Bun 1.3.14 自身带未修复的 brotli 解码器（架构师报告 §5.2），但构建阶段不使用 brotli 解码；这是"构建容器固有面"，不是本仓库脚本的调用路径。

#### 3.3.4 CVE-2026-105481（keep-alive 响应早于请求）

- 触发面是 Node **HTTP 客户端**与不可信/被攻陷的服务器之间的 keep-alive 连接。
- 构建脚本无 `http.request`/`https.request`；`openapi-typescript` 的输入是本地文件 URL（`generate-api-types.mjs:8,40`），不发起网络请求。
- 唯一的 Node HTTP 客户端是 Vite dev proxy，目标是开发者指定的后端（默认 `http://localhost:8080`，`vite.config.ts:6`）；响应源是自家 Go 后端，不是不可信第三方。
- **判定：不可达。**

#### 3.3.5 CVE-2026-106074（randomUUID/randomInt 熵缓存进 SEA/快照）

- 触发前提：用 Node 构建 **SEA（Single Executable Application）** 或 **startup snapshot**，且进程内使用 `crypto.randomUUID()`/`randomInt()`。
- 本仓库没有 `--experimental-sea-config`、`sea-config.json`、`--build-snapshot`、`NODE_OPTIONS`（§5 无输出）；生产是 Go 二进制，不涉及 Node SEA。
- 前端确实使用随机数，但那是**浏览器** `crypto.getRandomValues`（`src/frontend/src/lib/requester.ts:71`、`src/frontend/src/pages/chains/use-chain-form.ts:115`），不是 Node 的 `randomUUID`/`randomInt`，也不进 Node 快照。
- **判定：不可达。**

#### 3.3.6 CVE-2026-106070 / 106071 / 106073（ZipFile / Permission Model）

- 106070 只影响 26.x，且触发 API 是 Node 的 `ZipFile`（`node:zip`）。本仓库无 26.x 运行时、无 `node:zip`；shell 脚本里的 `unzip` 是独立二进制，不是 Node API。
- 106071/106073 的触发前提是启用 Node Permission Model（`--permission` + `--allow-fs-*`）。本仓库所有 Node 调用点都没有该参数，也没有 `NODE_OPTIONS` 注入（§5）。
- 106073 虽然影响 24.x/22.x，但"受影响版本"不等于"启用该特性"；本仓库未启用，因此不可达。
- **判定：均不可达。**

### 3.4 为什么 CI 的 Node 22.23.3 仍不可达（威胁模型）

即使承认 CI 确实用 Node 22.23.3 执行 `node scripts/*.mjs`，也不构成这些 CVE 的利用路径：

1. **脚本不调用触发 API**：四个 `.mjs` 的 import 清单是完整的（`node:fs/promises`、`node:path`、`node:url`、`node:zlib.gzipSync`；另有第三方 `openapi-typescript`，输入是本地文件 URL，见 `generate-api-types.mjs:8,40`），没有动态 `require`/`import()` 的兜底（已通读全文）。
2. **输入是仓库文件**：`docs/openapi.yaml`、`dist/assets`、`src/styles/tokens.css`。能改这些文件的 PR 作者，同样能改 `package.json`/脚本本身 —— 那已经是 CI 内的任意代码执行，不需要借这些 CVE。
3. **没有网络/解压/权限面**：脚本不发起请求、不解 brotli、不启用 Permission Model、不构建 SEA。
4. **`tsc`/`vite build` 是纯本地编译**：它们在 CI 由 Node 执行，但不使用 `dns`/TLS 客户端/Brotli 解码；`vite build` 的 gzip 统计是编码器。

因此，CI 的问题是**版本暴露 + 不可复现**，不是**可利用漏洞**。钉版本的价值在于：消除"到底受哪些 CVE 影响"的长期不确定性，并防止未来某个脚本无意中引入触发 API 时无人察觉。

### 3.5 为什么开发机 Node 24.21.0 是"真实暴露但非本仓库缺陷"

- 版本层面：24.21.0 < 24.22.1，受 8 个 CVE（含 106073）。
- 路径层面：开发机执行的仍是同一组构建脚本 + Vite dev server；前者不触达触发 API，后者只监听 localhost 且代理目标由开发者控制。
- 结论：**环境清债**（升级 Node）必要，但不应把它描述成"Lattix 代码漏洞"。真正要改的是让版本可声明、可检查（`engines` + `.node-version`），避免下一位开发者继续落在 24.21.0。

---

## 4. 代码级修补清单（按 CWE）

### 4.1 P1：真实可达 / 低成本消除版本暴露

| ID | CWE | 位置 | 缺陷 | 最小修复 | 可达性与影响 |
|---|---|---|---|---|---|
| P1-1 | **CWE-367 / CWE-918** | `src/shared/requester/guarded_dial.go:61-71,84-109` | `checkDialTarget` 用 `LookupIPAddr` 校验一次，随后 `dialer.DialContext(ctx, network, addr)` **按原主机名再解析一次**；两次解析之间可被 DNS rebinding 替换为内网 IP。`GuardedDialContext` 的注释明确说"先解析目标主机名…任一解析 IP 命中即拒绝"（`:56-58`），但实现没有把校验结果用于拨号。 | 解析一次，逐个校验后**直接拨 `ip:port`**；HTTP Transport 会按 URL host 设置 TLS `ServerName`，证书校验仍针对原主机名。见 §4.4 代码示意。 | **真实可达**：外部订阅（`main.go:470`）、Webhook（`alert.go:60-62`）、CDN catalog（`http_client.go:44`）都走该拨号器。攻击者控制订阅/Webhook 域名 DNS 时，可让面板访问内网 HTTP 服务（Webhook 允许 `http://`，`settings.go:402-410`），或对内网 HTTPS 服务做端口/存在性探测。需要 DNS rebinding（TTL=0/短 TTL）与一个可配置的第三方 URL，评为中危。 |
| P1-2 | **CWE-1395** | `.github/workflows/ci.yml:21-31`、`src/frontend/package.json:8-12` | CI 无 `setup-node`，`node scripts/...` 落到 runner 自带 Node 22.23.3（< 22.23.4，受 7 个 CVE）。runner 镜像迁移后还会漂移到 24.21.0（Ubuntu 26.04 README），仍受 8 个。 | 加 `actions/setup-node`（`node-version: 24.22.1`）、提交 `.node-version`、在 `package.json` 加 `engines.node: ">=24.22.1"`。 | 版本暴露，路径不可达（§3.4）。修复成本低，且让未来脚本改动有护栏。 |
| P1-3 | **CWE-1395** | `.github/workflows/release.yml:34-40` | `setup-bun` 未指定 `bun-version`；setup-bun 默认取 `package.json` 的 `packageManager`，否则取 **latest**。仓库没有 `packageManager`，因此 release 用漂移的 Bun，与 Docker/CI 的 1.3.14 不一致。 | 加 `with: bun-version: '1.3.14'`（短期可复现），或加 `packageManager: "bun@1.3.14"` 后用 `bun-version-file`；待 brotli 修复发版后再统一升级。 | 构建不可复现；release 产物与 CI 测试的运行时不同。当前不构成可利用路径（脚本不触达 brotli 解码）。 |

### 4.2 P2：需要设计/测试的加固

| ID | CWE | 位置 | 缺陷 | 最小修复 | 可达性与影响 |
|---|---|---|---|---|---|
| P2-1 | **CWE-1395** | `Dockerfile:3`、`.github/workflows/ci.yml:25` | Bun 1.3.14 的 Brotli 解码器缺 CVE-2026-111497 修复（Bun issue #46352；截至 2026-10-22T03:20Z 最新 release 仍为 1.4.3，维护者称"今天发修复版"）。 | 待 1.4.4+ 发布后升级 Docker/CI/release 三处；升级前在 release notes 确认包含 `google/brotli@0ed4268a`。 | 构建容器固有不安全组件；当前脚本不解 brotli，路径不可达。升级属清债。 |
| P2-2 | **CWE-295** | `src/backend/cmd/backend/main.go:471-477`、`src/backend/internal/extsub/service.go:153-157` | 外部订阅可选 `skip_cert_verify`，启用后走 `InsecureSkipVerify: true` 的客户端；MITM 可注入任意节点配置（服务器地址/协议/端口），进而影响订阅与下发。 | 保留功能但加护栏：UI 显式风险文案 + 二次确认；服务端审计日志记录"该订阅关闭了证书校验"；限制为管理员；长期支持自定义 CA/证书 pin 替代。 | 管理员显式开启才可达；属风险接受项，不是默认配置漏洞。缺的是可观测与误用防护。 |
| P2-3 | **CWE-1395** | `src/frontend/package.json:42`、`docs/development.md:27,35`、`docs/frontend.md:9-13` | `@types/node` 锁在 24.13.3（运行时 24.21/24.22）；文档统一写 `bun run build`，但脚本内部调用 `node`，开发者只装 Bun 且 PATH 无 `node` 时会失败。 | 升 `@types/node` 到 ^24.22；在文档中写明"构建脚本需要 Node ≥24.22.1（或改用 Bun）"。 | 类型/文档层，无安全影响；减少"以为用 Bun、实际用 Node"的认知偏差。 |

### 4.3 P3：低危/供应链/纵深防御

| ID | CWE | 位置 | 缺陷 | 最小修复 | 可达性与影响 |
|---|---|---|---|---|---|
| P3-1 | **CWE-409** | `src/agent/internal/xray/upgrade.go:167-190`、`src/backend/internal/panel/selfupdate/update.go:583-628` | `unzipOne`/`untargz` 在校验 SHA256 后无解压后大小上限；恶意镜像/被攻陷 release 可用 zip/tar bomb 撑满磁盘（下载侧有 512 MiB 上限，`external.go:176`，但解压可放大）。 | 解压时用 `io.LimitReader`/累计计数限制单文件与总解压量；超限删除临时目录并报错。 | 需先控制下载源或发布链；属 DoS 纵深防御。 |
| P3-2 | **CWE-494** | `src/agent/internal/selfupdate/selfupdate.go:93-99`、`src/backend/internal/panel/selfupdate/update.go:377-407` | 更新包与 `checksums.txt` 同源下载，SHA256 只能防传输损坏/非同源替换，不能防源站被攻陷；无签名校验。 | 发布产物加 cosign/minisign 签名或 GitHub Artifact Attestations；更新端验签后再替换。 | 需 GitHub release/镜像源被攻陷；TLS 已覆盖多数 MITM。 |
| P3-3 | **CWE-1395** | `Dockerfile:11` | Go 工具链 1.26.6，落后 CI/release 的 1.26.10；产物缺 go1.26.9/1.26.10 的 crypto/tls、crypto/x509 等修复。 | 改 `golang:1.26.10-alpine`。 | 架构师报告 §5.1 已列为 P1；本文只交叉引用，不重复取证。 |
| P3-4 | —（类型层） | `src/frontend/package.json:42` | `@types/node` 24.13.3 落后运行时。 | 升 ^24.22.x。 | 无安全影响。 |

### 4.4 建议补丁（示意，未编译）

**P1-1：把"校验过的 IP"用于拨号，消除 TOCTOU。** 现有 `GuardedDialContext` 的接口已经注入 `ipResolver`，便于写回归测试（让 fake resolver 第一次返回公网、第二次返回内网，断言拨号仍被拒）。

```go
func GuardedDialContext(resolver ipResolver) func(context.Context, string, string) (net.Conn, error) {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if os.Getenv(allowPrivateOutboundEnv) == "1" {
			return dialer.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ipAddr := range ips {
			if IsBlockedIP(ipAddr.IP) {
				return nil, fmt.Errorf("refusing to dial %s: resolves to blocked address %s", host, ipAddr.IP)
			}
		}
		// 只拨已校验的 IP；TLS ServerName 仍由 http.Transport 按 URL host 设置，
		// 因此证书校验不会退化为"按 IP 校验"。
		var lastErr error
		for _, ipAddr := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ipAddr.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no addresses for %s", host)
		}
		return nil, lastErr
	}
}
```

> 注意：上面的写法把"校验"和"拨号"绑定到同一份解析结果；`http.Transport` 在 `TLSClientConfig.ServerName` 为空时会用 URL 的 host 作为 SNI/校验名，所以按 IP 拨号不会破坏证书校验。若未来给 `TLSClientConfig.ServerName` 显式赋值，需要复核这一前提。

**P1-2：CI 钉 Node（示意）。**

```diff
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -21,6 +21,9 @@
       - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
+      - uses: actions/setup-node@v4
+        with:
+          node-version: 24.22.1
       - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2
```

**P1-3：release 钉 Bun（示意）。**

```diff
--- a/.github/workflows/release.yml
+++ b/.github/workflows/release.yml
@@ -34,2 +34,4 @@
       - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2
+        with:
+          bun-version: '1.3.14'
```

**P2-3/P3-4：版本声明（示意）。**

```diff
--- a/src/frontend/package.json
+++ b/src/frontend/package.json
@@ -3,6 +3,9 @@
   "private": true,
   "version": "0.0.0",
   "type": "module",
+  "engines": { "node": ">=24.22.1" },
+  "packageManager": "bun@1.3.14",
   "scripts": {
```

### 4.5 已核查且不构成问题的项（阴性结论，避免误报）

| 项 | 证据 | 结论 |
|---|---|---|
| Go CDN catalog 解压面 | `src/backend/internal/cdncatalog/catalog.go:160` 设 `Accept-Encoding: identity`；`:171-173` 拒绝任何非 identity 的 `Content-Encoding`；`:174-179` 限制读取大小 | 不存在"自动解压不可信响应"的路径；这是 Brotli/压缩类 CVE 的正确控制 |
| tar 解包路径穿越 | `src/backend/internal/panel/selfupdate/update.go:603-606` 有 `filepath.Clean` + 前缀校验 | 已防 tar slip（CWE-22） |
| zip 解包路径穿越 | `src/agent/internal/xray/upgrade.go:174-182` 用 `filepath.Base(f.Name) == name` 且写到固定 `dest` | 已防 zip slip；无解压大小上限是 P3-1，不是路径穿越 |
| 证书目录域名注入 | `src/backend/internal/panel/certdir.go:22-41` 的 `ValidTLSDomain` 拒绝 `/`、反斜杠、非法 label | `filepath.Join(dir, domain)`（`:45`）不可逃逸 |
| SPA 静态文件路径穿越 | `src/backend/cmd/backend/main.go:713-715` 用 `filepath.Clean` + `fs.Stat`（`fs.ValidPath` 语义） | 不可逃逸；`http.FileServerFS` 亦拒绝 `..` |
| Agent 的 `math/rand` | `src/agent/cmd/agent/runtime_settings.go:106,120` 仅用于重连抖动/±20% jitter | 非安全敏感随机数；不构成 CWE-338 |
| Agent 的 `InsecureSkipVerify` 探测 | `src/agent/internal/xray/fill.go:349-360` 注释"仅探测可达性"；返回值只用于挑选 Reality dest，不用于证书信任决策 | 风险接受，非漏洞 |
| 硬编码凭证 | 对 `src`/`scripts`/`.github` 扫描 `BEGIN * PRIVATE KEY`、`AKIA…`、`ghp_…`、`password/secret/api_key = "…"`：模式扫描未命中；唯一命中在 `src/frontend/dist`（gitignored 构建产物） | 未发现硬编码凭证；`dist` 不入库（`src/frontend/.gitignore:11`） |
| LLM/提示注入面 | 全仓仅 `src/backend/internal/testcatalog/international_targets.json:29` 出现 "OpenAI API" 测试目标，按关键词扫描未发现 LLM SDK/提示词链路 | 提示注入不适用 |

---

## 5. 复现命令

以下命令均可在仓库根目录执行；输出为 2026-10-22 本机实测。

### 5.1 Node 运行时与构建脚本

```bash
$ node -v
v24.21.0
$ bun -v
bash: bun: command not found

$ grep -n "from 'node:" src/frontend/scripts/*.mjs
src/frontend/scripts/check-build-chunks.mjs:1:import { readFile, readdir } from 'node:fs/promises'
src/frontend/scripts/check-build-chunks.mjs:2:import { resolve } from 'node:path'
src/frontend/scripts/check-bundle-budget.mjs:10:import { readdir, readFile } from 'node:fs/promises'
src/frontend/scripts/check-bundle-budget.mjs:11:import { resolve } from 'node:path'
src/frontend/scripts/check-bundle-budget.mjs:12:import { gzipSync } from 'node:zlib'
src/frontend/scripts/check-token-contrast.mjs:11:import { readFile } from 'node:fs/promises'
src/frontend/scripts/check-token-contrast.mjs:12:import { resolve } from 'node:path'
src/frontend/scripts/generate-api-types.mjs:1:import { readFile, writeFile } from 'node:fs/promises'
src/frontend/scripts/generate-api-types.mjs:2:import { resolve } from 'node:path'
src/frontend/scripts/generate-api-types.mjs:3:import { pathToFileURL } from 'node:url'

$ grep -rnE "dns\.|node:(dns|tls|https|http|net)|brotli|DecompressionStream|--permission|node:zip|randomUUID" \
    src/frontend/scripts src/frontend/src
(no output)

$ grep -n '"build"' src/frontend/package.json
8:    "build": "node scripts/generate-api-types.mjs --check && tsc -b && vite build && node scripts/check-build-chunks.mjs"

$ ls .node-version .nvmrc src/frontend/.node-version 2>&1
ls: .node-version: No such file or directory
ls: .nvmrc: No such file or directory
ls: src/frontend/.node-version: No such file or directory

$ grep -n '"engines"\|"packageManager"' src/frontend/package.json
(no output)

$ ls src/frontend/node_modules/undici 2>&1
ls: src/frontend/node_modules/undici: No such file or directory

# 构建工具链依赖是否触达敏感 API（输出为空表示未触达）
$ for d in vite typescript @vitejs @tailwindcss openapi-typescript @redocly; do
    grep -rlE "brotliDecompress|DecompressionStream|node:dns|node:tls" "src/frontend/node_modules/$d" --include='*.js' --include='*.mjs' --include='*.cjs' 2>/dev/null
  done
(no output)
```

### 5.2 CI / Release 版本

```bash
$ grep -n "setup-node\|setup-bun\|bun-version\|bun run build" .github/workflows/*.yml
.github/workflows/ci.yml:23:      - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2
.github/workflows/ci.yml:25:          bun-version: '1.3.14'
.github/workflows/ci.yml:30:      - run: bun run build
.github/workflows/release.yml:34:      - uses: oven-sh/setup-bun@0c5077e51419868618aeaa5fe8019c62421857d6 # v2
.github/workflows/release.yml:40:          bun run build

# runner 镜像的 Node 版本（ubuntu-24.04 当前 ubuntu-latest）
$ curl -s https://raw.githubusercontent.com/actions/runner-images/main/images/ubuntu/Ubuntu2404-Readme.md | grep '^- Node'
- Node.js 22.23.3
# 2026-11 起 ubuntu-latest 将指向 26.04；该镜像的 Node 仍是 24.21.0
$ curl -s https://raw.githubusercontent.com/actions/runner-images/main/images/ubuntu/Ubuntu2604-Readme.md | grep '^- Node'
- Node.js 24.21.0

# setup-bun 默认值（未写 bun-version 时）
$ curl -s https://raw.githubusercontent.com/oven-sh/setup-bun/main/README.md | grep -A1 'bun-version'
| `bun-version` | The version of Bun to download and install. | Version from `package.json`, or `latest` | ...
```

### 5.3 公告事实

```bash
$ curl -s https://nodejs.org/en/blog/vulnerability/october-2026-security-releases \
  | sed 's/<[^>]*>/ /g' | tr -s ' ' | grep -o 'CVE-2026-[0-9]*'
CVE-2026-105482
CVE-2026-105483
CVE-2026-106069
CVE-2026-111497
CVE-2026-105481
CVE-2026-106072
CVE-2026-106074
CVE-2026-106070
CVE-2026-106071
CVE-2026-106073

$ curl -s https://nodejs.org/dist/index.json | python3 -c 'import sys,json;print([(r["version"],r["date"]) for r in json.load(sys.stdin) if r["version"] in ("v24.22.1","v22.23.4","v26.12.1")])'
[('v26.12.1','2026-10-21'), ('v24.22.1','2026-10-20'), ('v22.23.4','2026-10-20')]
```

### 5.4 Go 侧的 SSRF 拨号路径

```bash
$ grep -n "LookupIPAddr\|DialContext" src/shared/requester/guarded_dial.go
66:	return func(ctx context.Context, network, addr string) (net.Conn, error) {
67:		if err := checkDialTarget(ctx, resolver, addr); err != nil {
70:		return dialer.DialContext(ctx, network, addr)
100:	ips, err := resolver.LookupIPAddr(ctx, host)
```

> 关键点：`:100` 解析一次用于校验，`:70` 用原始 `addr`（含主机名）再次拨号；两者之间是 TOCTOU 窗口。

```bash
$ grep -n "NewExternalHTTPClient\|GuardedDialContext" src/backend/cmd/backend/main.go src/backend/internal/alert/alert.go src/backend/internal/cdncatalog/http_client.go
src/backend/cmd/backend/main.go:470:	externalFiles := external.ExternalFileRequester{Doer: external.NewExternalHTTPClient(30 * time.Second)}
src/backend/cmd/backend/main.go:475:		DialContext:     external.GuardedDialContext(nil),
src/backend/internal/alert/alert.go:61:		Doer: external.NewExternalHTTPClient(sendTimeout),
src/backend/internal/cdncatalog/http_client.go:44:	client.Transport.(*http.Transport).DialContext = requester.GuardedDialContext(nil)
```

---

## 6. 残余风险与待确认项

1. **CI 的 Node 版本未在流水线日志中实测**：22.23.3 来自 runner-images 的 Ubuntu 24.04 README（当前 `ubuntu-latest`），不是本次 workflow 运行日志；建议按 P1-2 加 `node -v` 步骤后以实测为准。runner 迁移到 26.04 后版本会变，这正是钉版本的理由。
2. **DNS rebinding 未做真实环境利用验证**：本文是代码级判定（解析与拨号分离），未在本机搭建 TTL=0 的权威 DNS 复现。可利用性取决于解析器缓存与攻击者 DNS 控制；建议用 `ipResolver` 注入"第一次公网、第二次内网"的 fake resolver 写回归测试，先锁死修复。
3. **Bun 修复版本尚未发布**：截至 2026-10-22T03:20Z，GitHub releases API 最新为 `bun-v1.4.3`（2026-10-10）；issue #46352 已于 2026-10-22T02:13Z 关闭，维护者称"今天发修复版"。升级前需确认 release notes 含 `google/brotli@0ed4268a`。
4. **`skip_cert_verify` 的产品语义**：这是管理员显式功能，本文按"风险接受"处理；若产品定位要求"任何情况下不得关闭证书校验"，则应改为自定义 CA/pin，而不是保留开关。
5. **Node 20（Iron）**：本仓库未发现 20.x 运行时；若某台开发机/自建 runner 仍是 20.x，属"永久受影响"，需先升级到 24.22.1+。
6. **未覆盖**：业务授权/越权、SQL 注入、前端 XSS/CSRF、完整 npm 供应链、生产镜像逐层扫描（后者见架构师报告）。

---

## 附录 A：关键行号索引

| 文件 | 行号 | 内容 |
|---|---|---|
| `src/frontend/package.json` | 8-12 | 四个 `node scripts/...` 构建脚本 |
| `src/frontend/package.json` | 42 | `@types/node` ^24.13.2（lock 24.13.3） |
| `src/frontend/scripts/generate-api-types.mjs` | 1-5, 8, 40 | 仅 fs/path/url + 本地 openapi.yaml |
| `src/frontend/scripts/check-build-chunks.mjs` | 1-2 | 仅 fs/path |
| `src/frontend/scripts/check-bundle-budget.mjs` | 10-12, 49 | fs/path + `gzipSync`（编码器） |
| `src/frontend/scripts/check-token-contrast.mjs` | 11-12 | 仅 fs/path |
| `src/frontend/vite.config.ts` | 6, 28-48 | dev proxy 目标由 `VITE_API_PROXY_TARGET` 决定 |
| `src/frontend/src/lib/requester.ts` | 134, 194, 262, 328 | 浏览器 `fetch`（非 Node） |
| `.github/workflows/ci.yml` | 16-31 | frontend job；无 setup-node |
| `.github/workflows/ci.yml` | 23-25 | Bun 钉 1.3.14 |
| `.github/workflows/release.yml` | 34-40 | setup-bun 未钉版本；`bun run build` |
| `Dockerfile` | 3, 9 | `oven/bun:1.3.14-alpine`；`bun run build` |
| `Dockerfile` | 11 | `golang:1.26.6-alpine`（交叉引用架构师 P1） |
| `Dockerfile` | 24-42 | 最终层 alpine + Go 二进制，无 Node |
| `src/shared/requester/guarded_dial.go` | 61-71, 84-109 | SSRF 拨号防护；解析/拨号分离的 TOCTOU |
| `src/backend/cmd/backend/main.go` | 470, 471-477 | 外部订阅普通/跳过校验两套客户端 |
| `src/backend/internal/extsub/service.go` | 153-157 | `SkipCertVerify` 选择客户端 |
| `src/backend/internal/panel/settings.go` | 402-410 | Webhook 允许 http(s)，无主机名预校验 |
| `src/backend/internal/alert/alert.go` | 59-62 | Webhook 走 `NewExternalHTTPClient` |
| `src/backend/internal/cdncatalog/catalog.go` | 160, 171-179 | `Accept-Encoding: identity` + 拒绝非 identity + 大小上限 |
| `src/backend/internal/cdncatalog/http_client.go` | 39-45, 69-71 | 附加信任根 + GuardedDialContext |
| `src/agent/internal/xray/upgrade.go` | 167-190 | zip 解包；固定 dest，无解压上限 |
| `src/backend/internal/panel/selfupdate/update.go` | 583-628 | tar.gz 解包；有路径前缀校验，无解压上限 |
| `src/agent/internal/selfupdate/selfupdate.go` | 93-99 | checksums.txt 同源校验 |
| `src/backend/internal/panel/certdir.go` | 22-45 | 域名校验 + 证书路径拼接 |
| `src/backend/cmd/backend/main.go` | 710-730 | SPA 静态文件服务 |
| `src/agent/cmd/agent/runtime_settings.go` | 104-120 | `math/rand` 仅用于抖动 |
| `src/agent/internal/xray/fill.go` | 349-360 | 可达性探测的 `InsecureSkipVerify` |
| `docs/security/2026-10-21-nodejs-advisory-assessment.md` | 17-28 | 前置报告的 CVE 矩阵（106073 行需校正） |

## 附录 B：本次未覆盖

- 业务授权/越权（面板/Agent/订阅 token 的权限边界）。
- SQL 注入/命令注入（Go store、`exec.Command` 调用点）。
- 前端 XSS/CSRF/CSP。
- npm/Bun 依赖的完整漏洞扫描（仅核查了与 CVE 触发 API 相关的包）。
- 生产镜像逐层扫描与 CA 信任库核对（架构师报告 §3 已完成）。
- 真实 DNS rebinding 利用复现（需独立 DNS 环境）。

---

**审计边界声明**：本文只做只读代码审计，未修改业务代码；所有"建议补丁"均为示意，未经编译/测试。涉及运行时版本升级的动作，建议在独立分支验证后合入。
