# trendradar

从 GitHub Trending 抓取热门仓库，生成每日摘要并提交到指定仓库 / 开 PR。
核心是 Go 单二进制，通过 IPC 信封与 DSH 插件通信。

## 安装

```bash
go install github.com/morehao/trendradar/cmd/trendradar@latest
```

## 命令

```bash
trendradar version --json     # 握手：version / ipcSchema / digestSchema / providers
trendradar run --config config.yaml            # 抓取→解析→摘要→渲染→提交
trendradar run --config config.yaml --dry-run  # 只产出文件，不写盘、不提交
trendradar run --config config.yaml --period weekly --date 2026-11-01  # 指定周期
trendradar serve --addr 127.0.0.1:8787         # 以 HTTP 提供 digest
trendradar subscribe add owner/repo            # 订阅仓库（可多个），需 --config
trendradar subscribe list                      # 查看订阅，需 --config
trendradar subscribe remove owner/repo         # 取消订阅，需 --config
```

## 输出布局

产出按周期分层，日 / 周 / 月三榜可在同一天共存、互不覆盖：

```
trending/
├── index.md                              # 日/周/月 分节索引
├── daily/2026-10/2026-10-31.{md,json}
├── weekly/2026/2026-W44.{md,json}        # date=2026-11-01（ISO 周）
└── monthly/2026/2026-10.{md,json}
```

`--period` 取 `daily|weekly|monthly`（缺省用 `config.trending.since`）；
`--date` 传该周期的结束日（周榜周日、月榜月末）。分支名为
`trending/<period>/<name>`。

## 开发

```bash
make build          # -> bin/trendradar
make test           # go vet + go test
make test-eval      # 真实模型评估（读取 .env 里的 TRENDRADAR_EVAL_*）
```

## 摘要外部增强（可选）

默认 `summarize.enrich: none`，只用仓库名/描述/语言/Star/排名。设为 `repo`
会额外抓取 GitHub topics；设为 `readme` 再追加 800 字 README 摘录，用于提升
摘要具体性。抓取走仓库 HTML（topics + 默认分支）与 `raw.githubusercontent.com`
（README），不消耗 REST 60/h 配额；结果缓存 7 天，仅对摘要缓存未命中的仓库拉取，
单仓库失败逐仓库降级、不影响 run。

```yaml
summarize:
  enrich: repo            # none | repo | readme，默认 none
  readmeChars: 800        # README 截断字符数
  enrichCacheTTLHours: 168
  enrichConcurrency: 4
  enrichTimeoutSeconds: 5
  enrichBudgetSeconds: 8  # readme 模式默认 12
```

`run --json` 会新增可选 `stages` 与 `enrich` 字段，`<repo>/.git/trendradar-usage.jsonl`
每行也记录 `durationMs`、`summaryDegraded` 与 `enrich` 计数，便于观测与回退。
完整可复制配置见 [`config.example.yaml`](config.example.yaml)。

- 代码默认 `enrich: none`；生产是否开启由部署侧 `config.yaml` 决定，建议先开 `repo`。
- `readme` 在个别仓库可能出现长度/中文率校验降级（`summarySource=original`）；上线前用
  `TRENDRADAR_EVAL_ENRICH=readme make test-eval` 复测，降级率 > 2% 先回退 `repo`。
- 回退判据：`enrich.failed/requested > 5%`、`usage.totalTokens` 超过基线约 4 倍、或
  `summaryDegraded` 明显上升时，把 `enrich` 调回 `none`/`repo`。

## 仓库发版订阅（可选）

把一批自有仓库加入订阅后，`run` 会每日检查它们的新 Release（没有 Release 时回退 Tag），
生成中文变更摘要，写入数据仓 `<output.dir>/releases/` 并与 trending 走同一次提交/PR。

订阅列表是一份**数据仓内受版本控制的文件** `<output.dir>/subscriptions.yaml`
（默认 `digests/subscriptions.yaml`，示例见 [`subscriptions.example.yaml`](subscriptions.example.yaml)），
由 CLI 维护（`--config` 必填，`subscribe` 与 `run` 因此解析到同一个文件），不写进 config.yaml。

```bash
trendradar subscribe add morehao/trendradar morehao/golib --config config.yaml
trendradar subscribe list --config config.yaml
trendradar subscribe remove morehao/golib --config config.yaml
```

订阅的写入不碰工作区：`add`/`remove` 只把「意图」写进 `<repo>/.git/trendradar-subscriptions.pending`
（该位置不参与 `git status`，所以 `run` 的 `IsClean` 不会被判脏）。下一次 `run` 在切到当日分支后
把 pending 合并进 `<output.dir>/subscriptions.yaml`，**随产物走同一次 commit/PR**；只有 `commit` 与
`push` 都成功后才会清空 pending（push 失败则下次 `run` 重放，宁可重复一次 commit 也不丢订阅）。

```yaml
releases:
  enabled: true
  subscriptionsFile: subscriptions.yaml
  period: daily            # daily | weekly | monthly
  fallbackToTags: true
  baselineOnFirstRun: true # 首次只记基线，不补发历史
  perRepoLimit: 5
```

要点：

- 默认 `enabled: false`，对现有 trending 零影响；`enabled: true` 时才加载订阅。
- 空订阅/文件缺失时 `run` 跳过该阶段并 warning，不报错；订阅文件 YAML 损坏才 fail-fast。
- 订阅数量不限（配置上限 500）；releases 阶段耗时约 `4s × 订阅数 + 5s`，建议配置 `GITHUB_READ_TOKEN`。
- 迁移：目标分支还没有 `subscriptions.yaml` 时，首次 `run` 会用 pending / 旧本机文件导入；
  两边都有且不同则取并集并 warning（不覆盖）。
- `--file` 仅用于只读排查：`list --file x` 读该文件，`add`/`remove --file` 返回 `CONFIG_INVALID`。
- 发版摘要的 token 消耗在 `run --json` 的 `releases.usage` 里（恒存在）：
  `calls=0` 表示没发生模型调用（如全部命中缓存），`calls>0 && usageKnown=false`
  表示调用了但供应商没回用量——两者不要混为一谈。
- 仓库没有 Release 时回退 Tag，并用该 tag 的 commit message 作为摘要依据。
- 状态与摘要缓存放在 `<repo>/.git/trendradar-release-*.json`，天然不入库。
- `run --json` 会带 `releases` 报告块（契约类型 `ReleasesReport`，见 contract v0.3.0）；
  `trendradar-usage.jsonl` 额外记录一行 `stage="releases"`。
- 插件可经 IPC 管理订阅：`trendradar subscribe --json` 从 stdin 读一个
  `RunRequest`（`{"command":"subscribe","action":"add","repo":"o/r","configPath":"..."}`），
  回写 `RunResult`。`configPath` 必填，保证 `subscribe` 与 `run` 解析到同一个订阅文件。
- `serve` 提供只读接口：`GET /api/v2/releases`、`GET /api/v2/releases/{date}`、
  `GET /api/v2/subscriptions`。

## 本地密钥与测试

不要把 API key 写进 config.yaml。复制 `.env.example` 为 `.env`（已被
`.gitignore` 忽略）并填入：

```dotenv
DEEPSEEK_API_KEY=sk-...
DEEPSEEK_MODEL=deepseek-flash
DEEPSEEK_BASE_URL=https://api.deepseek.com/v1
```

`trendradar` 启动时会自动向上查找最近的 `.env.local` / `.env`，也可用
`TRENDRADAR_ENV_FILE=/path/to/file` 指定。真实 shell 环境变量优先于文件。
config.yaml 支持 `${VAR}` 引用，例如：

```yaml
model:
  provider: deepseek
  name: ${DEEPSEEK_MODEL}
  baseURL: ${DEEPSEEK_BASE_URL}
  apiKey: ${DEEPSEEK_API_KEY}
```

评估 harness 也用同一套变量：`.env` 里放 `TRENDRADAR_EVAL_PROVIDER/MODEL/
BASE_URL/API_KEY`，然后 `make test-eval`。加 `TRENDRADAR_EVAL_ENRICH=repo|readme`
即可复测带外部上下文的 A/B 摘要。

## 目录

- `cmd/trendradar` — CLI 入口与子命令
- `internal/ipc` — 与插件共享的 IPC 信封、错误码、版本信息
- `internal/config` — config.yaml 加载与校验
- `internal/subscription` — 订阅列表读写与校验
- `internal/releasewatch` / `internal/releasedigest` — 发版订阅抓取、摘要与文档
- `contract/` — 四份 schema 与 fixtures（Go 与插件共用，唯一真源）
- `.dsh/docs/` — 过程性文档（不入库）
