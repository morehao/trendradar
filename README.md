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
BASE_URL/API_KEY`，然后 `make test-eval`。

## 目录

- `cmd/trendradar` — CLI 入口与子命令
- `internal/ipc` — 与插件共享的 IPC 信封、错误码、版本信息
- `internal/config` — config.yaml 加载与校验
- `contract/` — 四份 schema 与 fixtures（Go 与插件共用，唯一真源）
- `.dsh/docs/` — 过程性文档（不入库）
