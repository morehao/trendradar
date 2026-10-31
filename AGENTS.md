# AGENTS.md — trendradar

本文件是本仓库对 AI 编码代理（及人类协作者）的**工作约定**。动手前先读完对应小节；
冲突时优先级：用户本次明确指示 > 本文件 > 目录内局部 `AGENTS.md`。

---

## 0. 最重要的一条：任何功能都可能有潜在消费者

**这是硬性规范，不是建议。** 判断一个改动「是否有外部影响」时，默认答案是**有**。

### 为什么

`trendradar` 早已不是「一个只给自己用的脚本」。它有三类外部入口，且历史上出现过
「以为没人读、后来真有人读」的情况：

| 入口 | 消费者 | 例子 |
|---|---|---|
| `trendradar run --json` | DSH 插件（`@morehao/dsh-github-trending`） | 读 `RunResult`、`files`、`branch`、`prUrl` |
| `trendradar subscribe --json`（stdin） | 插件 | 从 stdin 读 `RunRequest` 管理订阅 |
| `trendradar serve` 的 HTTP 接口 | 插件、未来的 Web 页面 | `/api/v2/digests/*`、`/api/v2/releases*` |
| 数据仓产物（`digests/**`） | 人、脚本、未来的聚合工具 | 发版 JSON/Markdown 被外部解析 |
| `@morehao/trendradar-contract` | 所有上述消费方 | Go module + npm 包 |

**已发生过的教训**（别重犯）：

- `v0.5.0` 把发版报告的 token 用量写成 5 个 `omitempty` 字段，全 0 时
  `json.Marshal` 得到 `{}`，消费端无法区分「没消耗」和「没上报」。
- 同一批字段后来改为契约里的 `ReleasesReport.usage`（必填），才把语义固定下来。

### 因此，任何改动都要自问

1. **谁会读到这个变化？** 列出至少：插件、serve 客户端、数据仓解析方、未来的自己。
2. **是加字段还是改字段？** 加可选字段是向后兼容；**改名 / 改类型 / 改语义 / 收紧校验**
   都是破坏性变更。
3. **对外形状由谁定义？** 凡是跨进程、跨语言、落盘或走 HTTP 的数据，**必须进
   `contract/`**，不允许在内核里「先随手加个字段」。
4. **零值是否可区分？** 计数/耗时/成本这类字段，「0」和「缺失」语义不同时，
   不要用 `omitempty`。
5. **本次没有消费者 ≠ 永远没有。** 拿不准时：进契约、给默认值、写 CHANGELOG。

> 违反这条通常不会让测试变红，但会在插件或 Web 上线时爆出来。评审时优先看这一条。

---

## 1. 仓库定位

- 从 GitHub Trending 抓取热门仓库 → 生成中文摘要 → 渲染 → 提交数据仓 / 开 PR。
- 可选**仓库发版订阅**：订阅自有仓库，每日检查新 Release（无 Release 回退 Tag），
  生成中文变更摘要，写入 `<output.dir>/releases/`。
- 核心是 Go 单二进制（`cmd/trendradar`），通过 IPC 信封与 DSH 插件通信。

## 2. 常用命令

```bash
make build          # -> bin/trendradar（带 version/commit ldflags）
make test           # go vet ./... && go test ./...
make lint           # golangci-lint run（本地需兼容 Go 1.26 的版本）
make test-eval      # 真实模型评估，读取 .env 里的 TRENDRADAR_EVAL_*
make snapshot       # goreleaser 本地快照

go test ./...                         # 全量测试
gofmt -l cmd internal tools           # CI 会检查此项必须为空
./bin/trendradar version --json       # 握手：ipcSchema / digestSchema / contractVersion

cd contract && npm ci && npm test     # 契约测试
cd contract && npm run generate       # 重新生成 Go/TS 类型
cd contract && npm run verify         # 生成后 git diff 必须为空（CI 门禁）
cd contract && npm run verify-versions # 版本载体自洽 + 破坏性变更门禁
```

## 3. 目录地图

```
cmd/trendradar/        CLI 入口与子命令（run / serve / subscribe / version / config）
internal/
  app/                 编排：摘要 → 渲染 → commit/push → PR；含 releases 阶段接入
  config/              config.yaml 加载、默认值、校验
  digest/              榜单领域类型（对 contract 的别名）
  releasedigest/       发版文档类型（对 contract 的别名）+ 内容哈希
  releasewatch/        发版订阅：增量判定、摘要、游标状态
  subscription/        subscriptions.yaml 读写与校验
  fetch/               GitHub HTTP（trending 抓取、REST 分页）
  enrich/              可选外部上下文（topics / README）
  summarize/           榜单摘要：prompt、缓存、校验
  render/              产物渲染（md/json/index）+ 稳定内容哈希
  serve/               只读 HTTP（digests / releases / subscriptions）
  pr/                  GitHub PR 最小客户端
  gitx/                git 操作封装
  ipc/                 IPC 信封、错误码、版本信息
contract/              独立 Go module + npm 包：所有对外数据形状的唯一真源
```

## 4. 契约（contract）规则

- **唯一真源**：`contract/schema/*.json`。Go/TS 类型由 `scripts/generate.mjs` 生成，
  **不要手改** `contract.gen.go` 与 `gen/ts/*`。
- **别名而非复制**：内核用 `type X = contract.X` 引用，禁止在 `internal/` 里再定义一份
  同名结构（`internal/digest`、`internal/releasedigest` 都是这个模式）。
- **wire 整数与包版本解耦**：`ipcSchema`/`digestSchema` 目前都是 `2`；新增**可选**字段
  只升包版本（`0.2.0 → 0.3.0`），提升 wire 整数会让所有旧插件握手失败。
- **`RunRequest` 是唯一严格对象**（`additionalProperties: false`），新增字段必须显式声明；
  条件规则用 `if/then` 表达（例如 `subscribe` 的 `action`/`repo`）。
- **生成器限制**：`allOf` 有两种形态——组合（首项是 `$ref`）与条件校验；新增 schema 时
  注意生成器只对前者产出嵌入字段。大整数（如 GitHub `id`）用 `"x-go-type": "int64"`。
- 改契约后必须：`npm run generate` → `npm test` → `npm run verify-versions` 全通过，
  并在 `contract/CHANGELOG.md` 记录。

## 5. 数据与副作用边界

| 数据 | 位置 | 是否入库 |
|---|---|---|
| 榜单/发版产物 | `<repo>/<output.dir>/…` | 是（经 PR） |
| 摘要缓存 | `<repo>/.git/trendradar-cache.json` | 否 |
| 发版游标 | `<repo>/.git/trendradar-release-state.json` | 否 |
| 发版摘要缓存 | `<repo>/.git/trendradar-release-cache.json` | 否 |
| 用量日志 | `<repo>/.git/trendradar-usage.jsonl` | 否 |
| 订阅列表 | `subscriptions.yaml`（相对 config 目录） | **否**（`.gitignore`）；向数据仓版本化演进中，见 §10 |
| 密钥 | `.env` / 环境变量 | 否 |

- **密钥永不落 config.yaml**：用 `${VAR}` 展开。
- `run --json` 的 `files` 与实际 `git add` 的目录必须一致（`output.dir` 是单一真源）。
- 未启用某阶段时，它必须**完全旁路**：不写文件、不改产物、不影响另一阶段的字节。

## 6. 代码约定

- Go 1.26；包内注释说明「为什么」，不写「做了什么」。
- 错误码走 `internal/ipc`：输入类问题用 `CONFIG_INVALID`，而不是 `INTERNAL`。
- 幂等优先：同一输入重复运行**不产生新提交**（`render.Comparable` + 内容哈希）。
- 外部不可信文本（Release notes、README）一律当**数据**：定界包裹、清洗、截断，不当指令。
- 测试：新增行为要有测试；涉及序列化的要有「零值不消失」类断言。

## 7. Git 与发布

- 受保护分支：`main`（及 `master`/`trunk`/`stable`/`develop`/`release/*`）。
  **禁止直接提交/推送**，走 `feat/*`、`fix/*`、`chore/*` 分支 + PR。
- 提交信息：`type(scope): 摘要`，正文说清**为什么**与影响面。
- 发布是 **tag 触发**：
  - 主程序：`v X.Y.Z` → `.github/workflows/release.yml` → goreleaser 出多平台二进制。
  - 契约：`contract/vX.Y.Z` → `.github/workflows/contract-release.yml` → npm 发布。
  - 发版前把 `internal/ipc/version.go` 的 `Version` 与 `CHANGELOG.md` 对齐；
    契约发版要同步 `contract/package.json`、`contract/version-map.json`、
    `internal/ipc/version.go` 的 `ContractVersion`。
- 版本语义：新增能力 → minor；行为修正 → patch；破坏性变更 → major（并说明插件升级路径）。

## 8. 文档

**分两类，落点不同，不要混放：**

| 类型 | 位置 | 入库 | 内容 |
|---|---|---|---|
| **交付性文档** | `docs/` | **是** | 整体架构（`docs/architecture.md`）、专题设计（`docs/specs/`）。这些是给团队/集成方看的终局文档 |
| 过程性草稿 | `.dsh/docs/` | 否 | 迭代草稿、排查笔记、临时计划 |

- `docs/architecture.md` 是**稳定结构**的单一入口：讲整体、讲不变量、讲契约；具体改动方案写 `docs/specs/<date>-<topic>-design.md`，与架构文档互链，不要把架构文档改成变更日志。
- 新增方案时必须：① 在 `docs/architecture.md` 开头的「专题方案索引」表里加一行；② 方案文档头部写状态块（状态 / 实施情况 / 作者 / 评审人 / 拍板人 / 最后更新）；③ 双向互链。
- 方案一旦入库，草稿区的同名副本应删除——**`docs/specs/` 是唯一版本**，避免两处维护分叉。
- 用户向：`README.md`、`config.example.yaml`、`subscriptions.example.yaml`。
- 变更记录：根 `CHANGELOG.md`、`contract/CHANGELOG.md`（两者都要维护）。
- 架构文档里的数字/路径/字段必须与代码一致；写之前先 `grep` 核对，不凭记忆。
- **示例文件不是数据**：`*.example.*` 只用于说明格式，不要写入真实订阅或密钥。

## 9. 已知未覆盖（改动前先确认是否撞上）

- 非 GitHub 平台（GitLab/Gitee）、私有仓库鉴权。
- commit 级 / PR 级 diff 订阅；webhook 秒级时效。
- `stages` / `enrich` 两个历史字段仍在契约外（`additionalProperties` 兜底），未排期。
- 订阅列表为单机文件，不跨机同步；团队共享方案待定（见下）。
- serve 只读、无鉴权；暴露到公网前必须补。

## 10. 待定决策

- **订阅数据的维护方式**：当前仍是本地 `subscriptions.yaml` + `.example` 模板。已有外部
  消费者（插件、serve、Web）后，示例文件 + gitignore 的模式在多机/团队场景下失效
  （换机器订阅就空、数据仓共享但订阅不共享）。方案已定并评审通过、**尚未实施**：
  [订阅数据版本化（方案 A）](docs/specs/2026-10-31-subscription-repo-versioned-design.md)。
  实施要点：`subscribe` 只写 `.git/` 下的 pending，`run` 切分支后落盘并随产物提交
  （直接写工作区会被 `IsClean` 拒绝）。**动订阅存储前先读该方案，不要默认沿用 example + gitignore。**
