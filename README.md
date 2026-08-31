# mem

> **面向 AI Agent 的可迁移网盘。**
>
> 开源 · 自托管 · 模型可插拔 · API / MCP / CLI / UI 共用一套记忆内核。

[![CI](https://github.com/fullstack-ai-infra/mem/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/fullstack-ai-infra/mem/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-experimental-orange.svg)](#project-status)
[![MCP Server](https://img.shields.io/badge/MCP%20Server-26%20tools-blue?logo=modelcontextprotocol)](docs/mcp.md)
[![smithery](https://smithery.ai/badge/@fullstack-ai-infra/mem-mcp)](https://smithery.ai/server/@fullstack-ai-infra/mem-mcp)

**A portable, self-hosted memory plane for AI agents.**

`mem` keeps files, metadata, and embeddings under your control and exposes the
same core through an HTTP API, a command-line client, an MCP server, and a web
interface.

**mem** 是用户自有的可迁移 Memory Plane：人把文件、照片、录音和笔记放进去，
Agent 也可以写回任务状态、观察、决定和产物。切换 Claude Code、Codex 等 Agent，
或换到另一台电脑时，任何获授权的 Agent 都能找回带出处的上下文并继续工作。

AI 网盘是 mem 面向人的信任界面。当前 Web 已能浏览/下载原件、自然语言搜索，
查看结构化记忆与不可变任务 checkpoint，执行反馈、归档、恢复和确认遗忘，并通过
Workspace Transfer 导出或 fresh 恢复同一份数据。不可变 correction/supersede、
增量同步和 merge restore 仍在路线中。API、MCP 和 CLI 是同一套能力面向 Agent
与自动化的入口。

项目的北极星、迁移范围与当前差距见 **[GOAL.md](GOAL.md)**。

mem **不是**：

- 聊天产品；回答由上层 Agent 负责，mem 负责提供可追溯上下文。
- Agent runtime 或工作流编排器；mem 不接管规划、工具循环和模型执行。
- 只上传 PDF 再聊天的通用知识库；原始多模态资产、长期关系和 Agent 写回同等重要。

```mermaid
flowchart LR
    H["人"] --> UI["AI 网盘 UI"]
    A["任意 Agent"] --> MCP["MCP / API"]
    O["自动化与运维"] --> CLI["CLI"]
    UI --> MEM["mem Memory Plane"]
    MCP --> MEM
    CLI --> MEM
    MEM --> SRC["原始资产与出处"]
    MEM --> IDX["结构化记忆、实体、关系与索引"]
    SRC --> CTX["带证据的 Context Pack"]
    IDX --> CTX
```

> [!WARNING]
> `mem` is experimental. Interfaces, storage schemas, and release artifacts may
> change without notice. Do not use it as the only copy of important data.

## 开始使用

面向第一次部署 mem 的人，主路径是 `deploy/compose`：一份密钥文件、一条 `up`、
一个浏览器。裸机进程栈是**只用于开发**的替代路径，见
[docs/RUN_LOCAL.md](docs/RUN_LOCAL.md)。

前置条件是 Linux 主机或 WSL2 / Docker Desktop 环境，装有 Docker Engine 与
Compose v2、`openssl`；模型无关的单机基线为 4 vCPU / 8 GiB 内存 / 80 GiB 磁盘。

```bash
git clone https://github.com/fullstack-ai-infra/mem.git
cd mem/deploy/compose
./generate-env.sh          # 生成 .env（0600，拒绝覆盖已有文件，含随机密钥）
docker compose --env-file .env -f compose.yaml config --quiet
docker compose --env-file .env -f compose.yaml up -d --build --wait
```

起来的是六个常驻服务（PostgreSQL/pgvector、Redis、MinIO、Worker、memd、Web）
加两个一次性任务（`minio-init` 建私有桶、`migrate` 恰好跑一次 schema）。只有 Web
对外暴露：`http://127.0.0.1:8080`，后端网络是 internal，数据库、对象存储与 gRPC
都不发布端口。

1. 打开 `http://127.0.0.1:8080` 注册首个账号。默认
   `MEM_REGISTRATION_MODE=first_user` 只放行第一个用户，第二次注册应被拒绝。
2. 在 Web 里新建目录（例如 `/Photos`）并上传文件，确认原件可浏览、可下载。
3. CLI 与 MCP 打的是同一套 API：拿到 token 后 `mem put`、`mem search`、
   `mem remember` 指向 `http://127.0.0.1:8080`。

`generate-env.sh` → `up -d --build --wait` → 首用户注册 → 建目录上传这条闭环已在
运维 dogfood 环境跑通，过程记录见
[#109](https://github.com/fullstack-ai-infra/mem/issues/109)。同时说清当前边界：
默认 AI profile `local-fast-v2` 只启用文本 embedding（`ollama:qwen3-embedding:0.6b`，
768 维）并只放行文本/PDF/结构化文本类 MIME，视觉 embedding、LLM、VLM、ASR 与
rerank 阶段是显式禁用的（视觉阶段在 CLIP 权重的安装、完整性与离线缓存探针纳入
模型清单之前保持禁用，避免首次请求时下载权重）。而 Compose 的 `backend` 网络是
`internal`，Worker 容器内的 `OLLAMA_BASE_URL` 又指向容器自身的 `127.0.0.1:11434`，
所以默认容器路径下这些模型阶段不可达：选择 `local-fast-v2` 时的 768 维 embedding
探针会失败并拒绝写入配置（fail-closed，不静默回退）。要让语义检索可用，需要显式
接入可达的 Ollama 或 BYOM provider；文件与原件下载、结构化记忆和词法召回不依赖
模型阶段。

密钥备份、端口与反向代理边界、备份/恢复演练、升级回滚以及多机 Helm 方案见
[生产部署指南](docs/DEPLOYMENT.md)。

## What is in this repository?

| Component | Purpose |
| --- | --- |
| `server/` | Go HTTP service (`memd`), CLI (`mem`), and stdio MCP server (`mem-mcp`) |
| `worker/` | Python gRPC worker for extraction, embeddings, and model providers |
| `web/` | React/Vite web interface |
| `docker-compose.yml` | Local PostgreSQL/pgvector, Redis, and MinIO dependencies |
| `deploy/compose/` | Production single-node deployment |
| `deploy/helm/mem/` | Production multi-node Kubernetes deployment |

The current implementation includes file and folder operations, token-based
access, search and retrieval surfaces, an extensible processing worker, and MCP
tools backed by the same service API. See [SPEC.md](SPEC.md) for the evolving
product and architecture contract.

## MCP Server

`mem-mcp` is a stdio MCP server exposing the mem memory plane to any MCP-compatible
Agent host (Claude Desktop, Claude Code, Codex, Cursor, Cline). It is a thin
adapter over the canonical `memd` HTTP API — one core across API / MCP / CLI / UI.

### Tools

| Tool | Description |
|------|-------------|
| `mem_put` | Upload content (text or base64 binary) and trigger AI indexing |
| `mem_get` | Read file content; binary returned base64-encoded, capped at 4 MiB |
| `mem_info` | File metadata + AI fields (caption / summary / tags / timeline_at / index_status) |
| `mem_file_annotation_decide` | Accept or reject one pending AI description/tag suggestion |
| `mem_list` | List files with filters (tag / mime-prefix / since / until / path-prefix) |
| `mem_ls` | List immediate subfolders + files under a folder path |
| `mem_mkdir` | Create folder (mkdir -p semantics) |
| `mem_mv` | Move file to a different folder, or rename in place |
| `mem_folder_tree` | Full folder tree as nested structure |
| `mem_remember` | Idempotently persist an observation, decision, preference, task state, fact, note or artifact reference |
| `mem_memory_list` | List bounded structured-memory summaries |
| `mem_memory_get` | Get one full structured memory by UUID within the token path boundary |
| `mem_feedback` | Record useful/not-useful or pin/unpin feedback with optimistic concurrency |
| `mem_archive` | Reversibly exclude a memory from normal recall |
| `mem_restore` | Return an archived memory to normal recall |
| `mem_forget` | Irreversibly redact one live memory payload after explicit confirmation |
| `mem_checkpoint` | Persist a versioned task checkpoint or an explicit handoff to another Agent/device |
| `mem_task_list` | List bounded resumable-task summaries |
| `mem_checkpoint_list` | List newest-first bounded checkpoint summaries for one task |
| `mem_checkpoint_get` | Get one immutable checkpoint and its full handoff payload |
| `mem_resume` | Restore the current task head or a selected historical checkpoint |
| `mem_search` | Natural-language search (text / visual / auto fuse); ranked files + snippets |
| `mem_context` | Build an evidence-backed context pack for the calling Agent |
| `mem_related` | Top-K files related to a `file_id` by embedding similarity |
| `mem_face` | Person clusters: `action=list` / `name` / `merge` |
| `mem_durable_context_recall` | Resume explicitly granted, workspace-scoped active memories for one principal |

### Quick start

```bash
# build
make build-mem-mcp            # produces ./bin/mem-mcp

# run with an existing memd + token
MEM_SERVER=http://localhost:8787 MEM_TOKEN=mem_... ./bin/mem-mcp
```

Register with Claude Code:

```bash
claude mcp add --scope user --transport stdio \
  --env MEM_SERVER=http://localhost:8787 \
  --env MEM_TOKEN=mem_... \
  mem -- /absolute/path/to/bin/mem-mcp
```

Full configuration, per-tool semantics, and host setup guides are in
[docs/mcp.md](docs/mcp.md).

## 五个高频场景

| 场景 | mem 提供什么 |
|---|---|
| **跨 Agent / 跨设备续接工作** | 找回上次做到哪里、关键决定及其依据，让新的 Agent 或新电脑接着做 |
| **精确查找个人证据** | 从合同、发票、证件、笔记中找金额、日期和原文位置 |
| **沉淀会议与沟通** | 把录音、纪要、聊天转成决定、行动项、人物和时间关系 |
| **复用偏好与约束** | 保存用户明确确认的偏好、长期约束和纠正记录 |
| **找回多媒体生活记忆** | 按人物、时间、地点和事件找照片、音频，并能打开原件 |

---

## 目标记忆闭环

mem 的核心不是一次 RAG 问答，而是以下五段闭环：

1. **写入**：保存原始资产、来源、时间、Agent/会话和访问范围。
2. **巩固**：解析、切块、抽取实体与时间，识别重复、冲突和前后版本。
3. **召回**：联合语义、关键词、实体、时间、路径和关系召回，再排序。
4. **使用**：按上下文预算返回原文片段、出处和置信度，由上层 Agent 决策。
5. **反馈**：记录采用、纠正、置顶和遗忘，推动后续巩固与排序。

当前已打通模型无关的
`remember → lexical context → feedback / archive / restore / forget`
控制闭环。自动巩固、纠正/替代关系和经过评测的版本化排序仍未完成，因此不会把
这些路线图能力描述成现有能力。

完整方向和阶段性验收标准见
**[docs/AGENT_MEMORY_DIRECTION.md](docs/AGENT_MEMORY_DIRECTION.md)**。

---

## API / MCP / CLI / UI 如何分工

| 表面 | 主要职责 |
|---|---|
| **API** | 唯一能力内核与稳定契约；所有客户端最终调用它 |
| **MCP** | Agent 的最短路径：写入、召回上下文、读取原件和反馈/生命周期控制 |
| **CLI** | 批量导入、脚本自动化、索引重建、调试、评测、导出与运维 |
| **UI** | AI 网盘与信任界面：浏览原件、理解召回、纠错、权限和遗忘 |

`mem_remember` 写入带来源的结构化记忆；`mem_search` 返回候选资产；
`mem_context` 从文件和结构化记忆中组装带证据的上下文。`mem_context` 不承担
聊天人格或 Agent 推理。

权限契约保持简单：`remember` 需要 `write`，`search/context` 需要 `search`，
读取/list memory 或原件需要 `read`；反馈、归档和恢复同时需要 `read + write`；
forget 需要 `delete` 及允许删除的 workspace 角色。写入时关联
`source_file_id` 还需 `read`。
`source=all` 的调用方必须检查 `partial/warnings`，避免把单路降级当成完整召回。

## 开发环境（仅开发）

改代码才需要这一节。只是想把 mem 跑起来，请回到[开始使用](#开始使用)。

工具链 pin 与平台无关，两个系统用同一套安装方式；`make bootstrap` 已经带 Linux
分支（Playwright Chromium 在 Linux 上加 `--with-deps`）：

| 依赖 | pin | 为什么是这个值 |
| --- | --- | --- |
| Go | 1.25.x | `server/go.mod` 的 `go 1.25.0` |
| Python + `uv` | 3.11 或 3.12，锁在 `worker/uv.lock` | Worker 的 protobuf 与测试依赖按 `uv sync --locked` 还原 |
| Node.js + npm | 24.x，锁在检出的 `web/package-lock.json` | Web 构建与浏览器验收 |
| protobuf 代码生成 | `protoc` 34.1、`protoc-gen-go` v1.36.11、`protoc-gen-go-grpc` v1.6.2 | 只有改 `worker/proto/*.proto` 才需要；两个 Go 插件用 `go install …@v1.36.11` / `@v1.6.2` 安装，平台无关。`make proto-python` 走 `grpcio-tools`，不需要系统 `protoc` |
| Docker 与 Compose v2 | 任意受支持版本 | 跑依赖容器、集成测试和生产 Compose 验收 |

### 依赖服务的平台等价

裸机开发栈（`bash scripts/dev_up.sh` / `bash scripts/dev_down.sh`，运行时数据落在
`.dev/`）在脚本层已经是跨平台的：`BREW_PREFIX` 会解析 `/opt/homebrew`、`/usr/local`
和 `/home/linuxbrew/.linuxbrew`，pgvector 模块同时接受 Linux 的 `vector.so` 与 macOS
的 `vector.dylib`，守护进程在 Linux 上用 `setsid` 脱离会话（`scripts/dev_up.sh:28-37`、
`:96-140`）。它假设 PostgreSQL 来自 **Homebrew 布局**，所以 Linux/WSL2 上要么装
Linuxbrew，要么改用依赖容器：

| 步骤 | macOS | Linux / WSL2 |
| --- | --- | --- |
| PostgreSQL 17 + pgvector | `brew install postgresql@17 pgvector`（keg-only，不需要 sudo） | 装 [Linuxbrew](https://docs.brew.sh/Homebrew-on-Linux) 后同一条命令即可，`dev_up.sh` 的默认前缀就是 `/home/linuxbrew/.linuxbrew`。用 apt 的 `postgresql-17` + `postgresql-17-pgvector` 时 `dev_up.sh` 找不到 keg（`detect_pg` 只查 brew 前缀），改走下一行的依赖容器路径 |
| 不装 Homebrew 的替代 | — | `make up` 起 `pgvector/pgvector:pg16`（`:5432`）、Redis（host `:6479`）、MinIO（host `:9100`），再按 `server/.env.example` 与 `worker/.env.example` 里的同一组默认值显式导出环境变量，用 `make server`、`make worker`、`make web` 以进程方式跑应用 |
| MinIO + `mc` | `brew install minio minio-mc` | 直接取官方 Linux 二进制：`curl -fL -o .dev/bin/minio https://dl.min.io/server/minio/release/linux-amd64/minio`（`mc` 同理，然后 `chmod +x`）。`dev_up.sh` 依次在 `.dev/bin/`、brew、PATH 中查找 |
| Redis | 可不装：`MEM_REDIS_URL` 留空时 memd 用进程内 fallback | 同左；要真队列或 SaaS 防重放时用 `make up` 里那个 `:6479` |
| Worker 依赖 | `cd worker && uv sync --extra clip` | 同一条命令（`--extra clip` 只为可选的图搜图装 CLIP） |
| `memd` / `mem` 二进制 | `make build-memd build-mem` | 同一条命令 |
| Ollama（要验收语义检索时） | 本机监听 `localhost:11434` | WSL2 里 `localhost:11434` 只有在 Docker Desktop WSL2 集成、Windows 侧 Ollama 设了 `OLLAMA_HOST` 监听、或启用镜像网络时才可达。不可达时 `mem profile select local-fast-v2` 的 768 维 probe 会失败并且不写入配置——这不是回退，是 fail-closed |

完整的日常开发流程、seed 数据、profile 边界与排查见 [docs/RUN_LOCAL.md](docs/RUN_LOCAL.md)；
可重复的回归门槛见 [docs/TESTING.md](docs/TESTING.md)。

## 为什么是 Agent-Native

```bash
# 未选择 workspace profile 的旧兼容路径可检索候选资产
# （该路径的默认 CLIP 当前只以英文作为已验收基线）
mem search "a golden retriever standing on green grass" --format json

# Agent 宿主启动独立的 stdio MCP 适配器
MEM_TOKEN=mem_... ./bin/mem-mcp

# Agent 幂等写入一条可追溯决定
mem remember "合同复核优先检查自动续费条款" \
  --kind decision --path /Contracts \
  --idempotency-key contract-review-renewal-v1 --agent-id codex

# 为下一步任务准备上下文，而不是在 mem 内启动聊天
mem context "继续上次的合同审阅" --source memory --scope /Contracts
```

每项核心能力应在 API 中只有一份语义，并按需要暴露给 MCP、CLI 和 UI。Agent
不是被动读取文件的访客，而是受权限约束的高频记忆使用者与写入者。

---

## 与相邻项目的边界

| 项目 | 核心对象 | mem 借鉴什么 | mem 的不同选择 |
|---|---|---|---|
| [Tencent/WeKnora](https://github.com/Tencent/WeKnora) | 文档知识平台、RAG、Agent 与自动 Wiki | 解析、混合检索、引用、评测和 Agent-first CLI/MCP | 面向个人长期数据与跨 Agent 写回，不以知识库问答/Agent runtime 为产品中心 |
| [mem0ai/mem0](https://github.com/mem0ai/mem0) | 通用 Agent memory layer | 结构化记忆、跨会话召回和反馈 | 同时保存可浏览的原始文件、照片、录音及其 AI 网盘体验 |
| [letta-ai/letta](https://github.com/letta-ai/letta) | 有状态 Agent runtime | 长期状态与上下文管理 | 不运行 Agent；作为任意 Agent 可替换的外置记忆后端 |
| [khoj-ai/khoj](https://github.com/khoj-ai/khoj) | 个人 AI、聊天与 Agent | 私有化个人知识体验 | 不绑定聊天入口或 Agent 人格，优先开放协议和数据可迁移性 |
| [nextcloud/server](https://github.com/nextcloud/server) | 文件同步与协作 | 用户掌控原件和成熟文件体验 | 在文件之上增加多模态理解、关系召回和 Agent 原生读写 |

mem 的壁垒不是绑定某个更大的模型，而是长期积累的、用户可校正的多模态原件，
时间—人物—事件关系，跨 Agent 写回，以及真实使用反馈形成的个人化召回。

---

## CLI 闭环示例

前提：已经有一个跑起来的 mem 实例（推荐按[开始使用](#开始使用)起单机容器），
`mem` 与 `mem-mcp` 已构建，并且登录过：

```bash
mem auth login    # 或显式指定实例：mem --server http://127.0.0.1:8080 auth login
```

项目仍处于 Phase 1 MVP。导入、召回与交接的当前体验：

```bash
mem put ~/Photos --recursive
# 可选：同步端附带可信的拍摄时间、位置和来源；AI 建议稍后在 Web 中确认
mem put ~/Photos/IMG_0001.jpg \
  --captured-at 2026-07-29T08:00:00+08:00 \
  --lat 31.2304 --lon 121.4737 --place Shanghai \
  --source-kind mobile --source-name "camera sync"
mem search "a golden retriever standing on green grass"
mem remember "照片导入已完成" --kind task_state --path /Photos \
  --idempotency-key photos-imported-v1
mem context "照片导入做到哪里了" --source memory --scope /Photos

# 一个 Agent 交接，另一个 Agent/新会话按稳定 task key 恢复
mem checkpoint --input handoff.json --idempotency-key photos-handoff-v1
mem resume photos/import

# 跨 mem 部署或电脑保存完整、可校验的工作区包
mem workspace export --output agent-workspace.membundle
```

单机 Compose（见上文[开始使用](#开始使用)）和多机 Helm 是同一份能力的两种部署
形态；密钥、迁移、高可用、备份恢复与升级边界的完整口径见
[生产部署指南](docs/DEPLOYMENT.md)。默认视觉模型的真实英文/中文边界见
[自然语言搜图基线](docs/acceptance/VISUAL_SEARCH_BASELINE.md)。

---

## 近期路线

1. **跨 Agent 接续**：`handoff / checkpoint / resume` 的版本化契约、CAS/幂等
   持久化和 API / CLI / MCP 已落地；继续扩充真实宿主验收与适配体验。
2. **跨设备恢复**：bundle v2（兼容导入 v1）与 API / CLI / Web `fresh`
   export/import 已落地；
   下一步是 `merge_conservative`、增量包、断点上传与本地同步。
3. **自然语言搜图**：持续增强视觉向量、caption、人物、时间、地点和事件的混合召回。
4. **Agent 记忆协议**：`remember / context / feedback / forget` 控制闭环已
   落地；下一步是不可变纠正/替代、巩固和经过评测的版本化排序。
5. **可见可控**：结构化记忆、任务交接、Resume 与 Workspace Transfer 视图已
   落地；继续补 correction/supersede、导入历史和更完整的权限管理。
6. **召回可信**：继续修通索引、关系、精确引用、rerank 和上下文预算。

当前文本向量会记录实际 provider；无法证明来源的旧向量保持
`legacy:unknown`，需通过 `mem provider reindex` 显式重建，绝不猜测模型身份。
结构化记忆的关键词/词法闭环不要求向量模型；自部署可选择本地或 BYOM
embedding，平台托管 embedding 则使用独立的 workspace 权益和额度。

项目暂不扩张为聊天前端、Agent runtime、通用工作流平台或连接器大集合。

---

## 设计文档

- 项目北极星与迁移目标：[GOAL.md](GOAL.md)
- 产品、架构和当前实现规范：[SPEC.md](SPEC.md)
- Agent 记忆产品方向：[docs/AGENT_MEMORY_DIRECTION.md](docs/AGENT_MEMORY_DIRECTION.md)
- 结构化记忆记录决策：[docs/adr/0001-agent-memory-records.md](docs/adr/0001-agent-memory-records.md)
- MCP 接入与工具约定：[docs/mcp.md](docs/mcp.md)
- 托管向量权益、幂等计费与隐私边界：[docs/MANAGED_EMBEDDINGS.md](docs/MANAGED_EMBEDDINGS.md)
- 北极星可重复验收：[docs/acceptance/NORTH_STAR.md](docs/acceptance/NORTH_STAR.md)
- Workspace bundle 决策：[docs/adr/0004-workspace-bundle.md](docs/adr/0004-workspace-bundle.md)
- 部署：单机 Compose 与多机 Helm：[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)
- 开发用裸机进程栈与 smoke test：[docs/RUN_LOCAL.md](docs/RUN_LOCAL.md)
- 项目开发边界：[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)
- 测试环境与回归门槛：[docs/TESTING.md](docs/TESTING.md)
- 参与贡献：[组织贡献基线](https://github.com/fullstack-ai-infra/.github/blob/main/CONTRIBUTING.md)
  与 [mem 开发契约](docs/DEVELOPMENT.md)
- 版本变化：[CHANGELOG.md](CHANGELOG.md)

---

## 仓库结构

```text
mem/
├── GOAL.md                   ← 项目北极星、迁移边界与验收场景
├── SPEC.md                   ← 当前产品、架构和接口规范
├── server/                   ← Go 主服务（API / CLI / MCP Server）
├── worker/                   ← Python AI Worker（Processor + Provider）
├── web/                      ← AI 网盘 UI
├── docs/                     ← 产品方向、开发、测试与接入文档
├── docker-compose.yml        ← 本地开发基础设施
└── docker-compose.test.yml   ← 一次性 PostgreSQL 回归环境
```

完整的本地进程栈启动、Worker、Web 和 smoke test 步骤（**仅开发**，需要
Homebrew 布局的 PostgreSQL）见 [docs/RUN_LOCAL.md](docs/RUN_LOCAL.md)；要把 mem
部署起来用，走[开始使用](#开始使用)与 [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md)。

Web 开发模式可单独启动：

```bash
cd web
npm ci
npm run dev
```

The service requires a development user and token before protected operations
can be used. Follow [docs/RUN_LOCAL.md](docs/RUN_LOCAL.md) for the complete
local setup and smoke test. For agent integration, build `mem-mcp` and follow
the [MCP setup guide](docs/mcp.md).

## Verify a change

CI applies the following checks to pull requests. Run the applicable groups
locally before opening one; [the mem development contract](docs/DEVELOPMENT.md)
and [testing guide](docs/TESTING.md) define the evidence expected when a check
cannot be run.

Go integration tests require an explicitly disposable PostgreSQL database
whose name ends in `_test`. With the Docker Compose services running, this
creates `mem_test` if needed:

```bash
make up
docker compose exec -T postgres sh -c \
  "psql -U mem -d postgres -tAc \"SELECT 1 FROM pg_database WHERE datname='mem_test'\" | grep -q 1 || createdb -U mem mem_test"
```

Run the Go protobuf, formatting, vet, migration, race/coverage, and build
checks:

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
PATH="$(go env GOPATH)/bin:${PATH}"
export PATH
make proto-go
git diff --exit-code -- server/internal/workerpb

cd server
test -z "$(gofmt -l .)"
go vet ./...
export MEM_TEST_DB=postgres://mem:mem@localhost:5432/mem_test?sslmode=disable
go run github.com/pressly/goose/v3/cmd/goose@v3.22.1 \
  -dir internal/db/migrations postgres "${MEM_TEST_DB}" up
go test -race -p 1 -coverpkg=./... -covermode=atomic \
  -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
cd ..
make build
```

Run the Worker protobuf, coverage, and package checks:

```bash
cd worker
uv sync --frozen --extra test --extra dev
make proto
git diff --exit-code -- mem_worker/proto
uv run pytest --cov=mem_worker --cov-report=term-missing \
  --cov-report=xml:coverage.xml
uv build
```

Run the Web checks:

```bash
cd ../web
npm ci
npm run audit
npm run lint
npm run typecheck
npm run build
```

Go and Python tests report coverage in CI. The web package currently uses
linting, type checking, and a production build as its required baseline.
Dependency auditing is also required: production advisories fail at moderate
severity, while development-only advisories fail at high severity.

## Contributing

All changes follow an issue-first, pull-request-only workflow:

1. Open or select an issue and record its type, evidence, scope, and acceptance
   criteria; bugs also receive an impact severity.
2. Develop on a branch linked to that issue.
3. Add tests and verification evidence with the change.
4. Open a pull request that closes or references the issue.
5. Obtain an independent review and pass required CI checks before merge.

Read the
[organization contribution baseline](https://github.com/fullstack-ai-infra/.github/blob/main/CONTRIBUTING.md)
and the [mem-specific development contract](docs/DEVELOPMENT.md), then use
[docs/maintainers/triage.md](docs/maintainers/triage.md) for the issue
taxonomy. Security reports must follow [SECURITY.md](SECURITY.md), not a
public issue. Release maintainers should use
[docs/maintainers/releasing.md](docs/maintainers/releasing.md).

## Project status

`mem` is in active experimental development. The repository is establishing a
stable contribution, test, and release baseline before committing to broad
distribution channels or compatibility guarantees.

## License

[Apache License 2.0](LICENSE) — 应用层永远完整可自托管，没有“开源阉割版”。

Copyright © 2026 mem contributors.
