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
- `contract/` — 四份 schema 与 fixtures（Go 与插件共用，唯一真源）
- `.dsh/docs/` — 过程性文档（不入库）
