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
trendradar serve --addr 127.0.0.1:8787         # 以 HTTP 提供 digest
```

## 开发

```bash
make build          # -> bin/trendradar
make test           # go vet + go test
```

## 目录

- `cmd/trendradar` — CLI 入口与子命令
- `internal/ipc` — 与插件共享的 IPC 信封、错误码、版本信息
- `internal/config` — config.yaml 加载与校验
- `contract/` — 四份 schema 与 fixtures（Go 与插件共用，唯一真源）
- `.dsh/docs/` — 过程性文档（不入库）
