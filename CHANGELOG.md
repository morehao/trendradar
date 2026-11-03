# Changelog

## Unreleased

### Added — 同一天的多周期可合并到一条分支（`output.branchGrouping`）
- 现象：`schedule.periods` 同时跑日/周/月时，每个周期各建一条分支；三条分支都改写同一份
  `<branchPrefix>index.md`，先合入的 PR 落地后，后面的 PR 必然冲突。
- `internal/config`：新增 `output.branchGrouping`，取值 `per-period`（默认）/ `per-date`。
- `internal/app`：`per-date` 时分支名为 `<branchPrefix><date>`，其余分支与索引逻辑不变，
  沿用既有的「分支已存在就追加」，索引在同一条分支上自然累积成三节。
- `internal/subscription`：`RunBranchName` 委托给新增的 `RunBranchNameGrouped`，`run` 与
  `subscribe` 共用一套分支名，避免两处各写一份。
- `internal/pr`：新增 `UpdateBody`；复用已有 PR 时只更新正文，不再用最后一个周期覆盖标题。

### 兼容性
- 默认 `per-period`：不写这个键时，分支命名、PR 数量、索引行为与现在完全一致。
- 契约未改：`contract/` 无改动，`ipcSchema` / `digestSchema` = 2。
- 配置走非严格 YAML 解析，旧核心收到该键会忽略、不报错。

## 0.9.1

### Fixed — 手动重建已合并日期时，PR 永久冲突
- 现象：对已合并的日期点「重新生成这一期」，core 复用旧分支 append；主分支是
  squash 合并（同一批文件有新提交），分支仍基于旧 base → 新 PR 永远 `CONFLICTING`，
  只能手工关掉再删分支。
- `internal/app`：manual 触发且该分支的 PR 已 merged / closed（分支已花掉）时，
  改成从 `origin/<base>` 重建分支（`switch -C`）并 `--force-with-lease` 推送；
  open PR 仍保持 append（不重写在审历史），schedule 行为不变。
- `internal/gitx`：新增 `PushForce`（仅用于这条重建路径）。

### Fixed — 发版阶段覆盖了榜单 digest 的日期 / 周期
- `internal/app`：`releasewatch` 的 `drel.Date` / `drel.Period` 不再写回 trending 的
  `d`。此前同时启用订阅的组合运行会把 PR 标题/正文写成 release 文档的「周末 +
  weekly」（月榜 PR 显示成 `trending weekly 2026-11-08`），也污染 `Result.Digest`。

## 0.9.0

### Changed — 榜单文档随 `RunResult` 回传（ipc v3）
- 背景：插件原先在运行结束后**读用户仓库的工作树**取本期 digest 做预览。0.8.6 起 PR 自动合并后会
  切回 base 并删除分支，本地 base 往往落后于远端，导致读到的文件缺失或还是旧版，预览被静默跳过
  （表现：面板里周/月榜长期停留在旧副本，缺少新排版里的 `## all` 分块）。
- `contract` 0.6.0：`ipcSchema` 2 → 3；`RunResult` 新增可选 `digest`
  `{path, bytes, sha256, text}`，即本次渲染出的 markdown 原文。`bytes`/`sha256` 与 `text` 的
  UTF-8 字节一致，`text` 上限 1 MiB。凡是渲染出 digest 的运行都会带（含 `skipped=unchanged`、
  `merged`、`pr-closed`，它们在 PR 预检之前就完成渲染）；dry-run、releases-only、渲染前失败不带。
- `internal/app`：`Result` 新增 `DigestPath`，渲染后从产物里认出本期 digest。
- `cmd/trendradar`：`run --json` 新增 `digest` 字段（`digestArtifact`），复用与 `files` 同一份
  字节计算 sha，不再需要调用方二次读盘。

### 兼容性
- **破坏性**：`ipcSchema` 2 → 3（`digestSchema` 保持 2）。新旧核心/插件不能混搭，插件握手会直接
  拒绝；插件侧随 0.9.0 升级契约依赖与预览实现。
- `digests/` 目录布局、`contentHash`、`files` 形状均不变。

## 0.8.7

### Fixed — 降级告警只说「哪个语言失败」，不说「为什么」
- 现象：运行记录里只有「以下语言抓取失败，已跳过：python, typescript」或
  「趋势页抓取失败，已回退到 GitHub 搜索 API」，无法区分 429 限流、超时、连接重置
  或页面改版，排障只能靠猜。
- `internal/fetch`：`TrendingResult` 新增 `Failures []FailedLang`（`Lang` + 归一化的
  `Reason`），失败语言按语言名排序以保证告警文本稳定；新增 `Summarize`，把错误收敛为
  `status 429` / `timeout` / `EOF` / `connection reset` / `dns error`，无法归类则截断保留原文。
- `internal/ingest`：`Result.DegradedDetail` 记录 `lang=reason` 列表；「页面抓到了但解析
  不出条目」单独说明（页面结构可能变化），不再与网络失败混为一谈。
- `internal/app`：降级告警追加「（原因：…）」；banner 文案不变。

### 兼容性
- `RunResult` 与契约形状不变（`warnings` 仍是 `[]string`），`ipcSchema` / `digestSchema` = 2。
- 告警文本变长（新增「（原因：…）」后缀）；按整串匹配的消费方需改为前缀匹配。

## 0.8.6

### Fixed — 手动「重新生成这一期」对已合并日期无效
- `internal/app/run.go`：PR 预检由「已合并无条件跳过」改为「`Merged() && trigger != manual`
  才跳过」。此前无论 manual/schedule，`--force` 都绕不过预检（force 仅清摘要缓存），
  导致已合并日期永远无法重建。现在：schedule 仍不重开已定稿的一天；manual 继续重建，
  `openPR` 的 `FindOpen`（state=open）对已合并 PR 返回 nil → 开新 PR。

### Changed — 合并后清理分支（仅新合并）
- `internal/pr/pr.go`：新增 `Client.DeleteBranch`（`DELETE /git/refs/heads/<branch>`，
  404/422 视为幂等成功）。
- `internal/gitx/git.go`：新增 `Git.DeleteLocalBranch`（`git branch -D`，缺失不算错）。
- `internal/app/run.go`：`openPR` 后若确认已合并（新增 `branchMerged`），删除远端分支 +
  切回 base 后删本地分支，数据仓只留 base 分支。仅确认合并才删；`autoMerge` 关（PR 仍开）
  不删；删除失败只 warn，不影响已交付。

### 兼容性
- 对外数据形状不变：`ipcSchema` / `digestSchema` = 2，contract 包不升级。
- 语义补充：manual 重建仍走 append；内容与已合并版一致时 `SkipReason=unchanged`
  （插件已展示「内容无变化」，不再误报「已合并跳过」）。
- 已合并 PR 记录在 GitHub 永久存在；本版本仅在 manual 时不再因它跳过，并清理合并后的分支。

## 0.8.5

### Changed — 榜单文档改为「按请求语言分块」，`all` 单独成块
- `internal/render/markdown.go`：产出 `.md` 不再按「每个仓库自身主语言」分节，改为按
  `trending.languages` 里**请求的每一页语言**分块（一页一块，顺序同请求列表）；`all`（全局
  页）命中的仓库统一进单独 `## all` 块，不再按各自主语言拆散。空块保留（选了某语言但本次
  0 条时仍输出该节空表）。同一仓库既在 `all` 页又在分语言页时，两块都列出（跨页允许重复）。
- `internal/parse/parse.go`：`ParseAll` 额外返回按页视图 `byPage`（页 → 仓库，保页序、不跨页
  去重）；扁平 `repos` 仍跨页去重，wire 文档结构不变。
- `internal/app/run.go`：digest 的 `languages` 元数据改为**如实回显请求语言列表**（不再从
  实际抓到的仓库反推，避免冒出用户没选的语言）；`--input` 重放路径仍回退旧的按主语言分块。
- `internal/ingest/ingest.go` · `internal/render/files.go` · `cmd/trendradar/run.go`：透传按页视图并接线。

### 兼容性
- 对外数据形状不变：`ipcSchema` / `digestSchema` 仍为 2，`contract` 包不升级。
- 语义变化：digest 的 `languages` 字段含义由「本次实际抓到的语言集合」改为「本次请求的语言
  列表」。因 `languages` 参与 `contentHash` 基线，同一天重跑产生的 `contentHash` 会与旧版本
  不同（属修正，历史已落盘文件不重算）。
- 历史 md（旧版按主语言分块）与新 md（按请求页分块）可共存于同一 `digests/` 下，格式兼容。

## 0.8.4

### Fixed — 趋势抓取失败导致整批退化、每语言仅零星几条且「今日新增」全为 0
- `internal/fetch/trending.go`：`FetchTrending` 改为逐语言部分容错。原用 errgroup
  任一语言失败即整批作废并回退搜索 API；现返回 `TrendingResult{Pages, FailedLangs}`，
  只要有一个语言成功就用成功部分继续，全部失败才回退。一次网络抖动不再拖垮整批。
- `internal/fetch/client.go`：默认重试次数 2 → 4（ghttp 已内置指数退避），HTML 路径改用
  浏览器 UA，降低被判定为爬虫的概率。
- `internal/fetch/search.go`：搜索兜底由「近 7 天新建 + 总 star」一锅烩改为**按语言**
  分别查询（`language:<l> pushed:>昨日 sort=stars`），保留每语言维度，内部上限常量 30。
- `internal/ingest/ingest.go`：新增 `DegradedReason`（`html_partial` / `search_fallback`）
  与 `DegradedLangs`；部分语言失败时仍用成功数据继续，不再整体回退。
- `internal/render/markdown.go` · `internal/render/files.go` · `internal/app/run.go`：
  依据降级原因在 `warnings` 与 digest markdown 顶部注入可读降级横幅（搜索兜底无今日增量 /
  部分语言抓取失败），降级继续出榜但明确标注，杜绝静默出残榜。
- `internal/config/config.go`：移除 `trending.perLanguage`。HTML 正常路径本来就有多少收多少，
  该字段仅曾用于搜索兜底的 `per_page`；同步 `config.example.yaml`。

### Changed（破坏性配置）
- 核心 YAML 不再接受 `trending.perLanguage`（`yaml.Unmarshal` 会静默忽略旧字段，向后兼容）。
  榜单不再设每语言条数上限。契约 wire（ipcSchema/digestSchema=2）与对外数据形状不变。

## 0.8.3

### Fixed — 英文占比高的仓库被误判为摘要无效、降级为原文
- `internal/summarize/validate.go` 的 `validSummary` 去掉了「汉字占比 ≥ 40%」这一门槛，
  改为「摘要至少含一个汉字且不等于原文描述」。原门槛本意是拦截「模型把英文原文照抄回来、
  没有真正写中文摘要」，但它会误杀一类完全合法的摘要：仓库名/描述本身几乎全是英文专有名词
  （如 `AI Studio Playground` / `Build` / `OpenAI` / `Anthropic` / `Gemini` / `API`）时，
  即便模型写出通顺的简体中文摘要，汉字占比也只有 0.14~0.24，远低于 0.4，于是被判无效、
  重试也救不回，整条被降级为 `[原文]`（`summarySource=original`），并触发误导性的
  「模型不可用或返回了无效结果」告警。英文占比高本身不是问题，只要摘要是中文（含汉字）
  且不是照抄描述就应接受。
- 纯英文输出（模型照抄描述、未翻译，零汉字）仍会被拒绝，原拦截意图不变。
- 新增回归测试 `TestParseAndValidateAcceptsLatinHeavyChineseSummary` 与
  `TestParseAndValidateRejectsPureEnglishSummary`。契约 wire 与对外数据形状不变。

## 0.8.2

### Fixed — 握手 contractVersion 与契约包版本不一致
- `internal/ipc/version.go` 的 `ContractVersion` 由 `0.4.0` 修正为 `0.5.0`，与
  `contract/package.json`（`@morehao/trendradar-contract@0.5.0`）对齐。这是 v0.8.0/#30
  升级契约包时遗漏的同步：`trendradar version --json` 之前自报一个旧契约版本。仅影响自报
  字段，`ipcSchema`/`digestSchema` 仍为 `2`，wire 与消费方行为不变。
- 新增 `internal/ipc` 单元测试 `TestContractVersionMatchesPackage`：断言 `ContractVersion`
  恒等于 `contract/package.json` 的 `version`，避免今后再次静默漂移。

## 0.8.1

### Fixed — 发版订阅按周期分支读不到 subscriptions.yaml
- `subscription.ReadRepoWithFallback`：按周期分支（如 `trending/weekly/2026-W44`）读取
  `subscriptions.yaml` 时，若该分支或文件不存在，回退到基准分支（`repo.baseBranch`，默认
  `main`）再读一次；两者都缺才返回空。此前 `ReadRepoOnTargetBranch` 在周期分支缺文件时直接
  返回空，导致「订阅只提交在 main / 别的周期分支」时解析成 0 订阅，release-only 运行被
  `skipped`，插件侧「生成变更分析」跑不出内容。
- `cmd/trendradar` 的 `buildReleasesOptions`（run）与 `subscribe list` 改为传入
  `cfg.Repo.BaseBranch` 作为回退分支。release-only 仍「读但不合并/不落地 pending」，
  `--repo` 单仓过滤与 commit/PR 流程不变；契约 wire 与对外数据形状不变。

## 0.7.0

### Changed — 发版订阅改为「每周一次 + 一仓一份文档」
- 发版阶段从「每 tag 一次模型调用、每期一份周文档」改为**每周一次批量总结**：同一仓库这一周
  的全部新版本进入同一次模型调用，产出一段 `summaryZh`（默认上限 1200 字符，可配 500–2000，
  超长截断并计入 `releases.truncated`）。抓取不再受 `perRepoLimit` 限制，该配置只限制文档里
  列出的版本数（其余写「其余 N 个」）。
- **周闸门**：游标新增 ISO 周键（`2026-W44`，按 `releases.timezone` 计算）。同一周重复运行对
  该仓库不请求 GitHub、不调模型、不写文档；失败不写周键，因此同周重试仍可行、跨周不丢数据。
- **一仓一份文档**：`<output.dir>/releases/<owner>/<repo>-01.md`（每册都带两位序号，续册 `-02.md`），最新一周 prepend 到最上面，
  同一周重复写入是幂等的（标题已存在则跳过）。当前册超过 `releases.docMaxLines`（默认 600）
  时**下一次**写入才开 `-02.md`——切卷只看写入前，任何一节都不会被拆到两册。
  首行不是 `# owner/repo`（被人工改过）时拒绝写入并告警，不覆盖用户内容。
- 周文档（`ReleaseDigest`）保留并提供 `GET /api/v2/releases*`：`schema` 升到 2，
  `repos[]` 新增 `summaryZh` / `periodStart` / `periodEnd`；`ReleaseItem.summaryZh` 变为可选
  （旧文档仍可读，新文档不再写逐条摘要）。文件名仍是 `<年>/<ISO周>.{md,json}`，目录改成 `releases/_weekly/`，
  保持 `GET /api/v2/releases/{date}` 的既有语义。
- `releases.period` 语义改为「总结周期」，本版只接受 `weekly`（其他值 `CONFIG_INVALID`）；
  新增 `releases.timezone`、`releases.weekStart`、`releases.docMaxLines`、`releases.summaryMaxChars`。
- `releases.concurrency` / `releases.budgetSeconds` 支持 `auto`：并发 `clamp(4, ceil(N/10), 8)`，
  预算 `min(300, 30 + 3N)`；显式数字仍可覆盖。发版阶段按该并发抓取/总结（限流时取消剩余任务）。

### Added
- `subscribe list` 的 `SubscriptionList` 增加 `items[]`（`fullName`、`url`、`latestTag`、
  `latestSummaryAt`、`lastCheckedAt`、`status`），由订阅 YAML + 本机游标 + 文档索引拼出，
  不读文档正文；40 仓 fixture 约 8 KB。
- 新命令 `subscribe releases --repo owner/repo [--volume N]`：读某一册的 markdown 与卷索引
  （`SubscriptionDoc`）。IPC 结果放在 `RunResult.subscriptionDoc`，`subscribe list` 放在
  `RunResult.subscriptions`。
- `run --stages trending,digest,releases`（契约 `RunRequest.stages`）：`--stages releases`
  只跑发版阶段——不抓 trending、不渲染榜单、**不合并 pending 订阅**，只读 `subscriptions.yaml`，
  产物仍走现有 commit/PR 流程。
- `serve` 新增只读 `GET /api/v2/repos/{owner}/{repo}/releases?volume=N`，与 IPC 共用
  `releasedoc.Read`；`GET /api/v2/subscriptions` 追加 `items[]`。
- 契约 0.4.0（`contract/CHANGELOG.md`）：`SubscriptionList.items`、`SubscriptionDoc`、
  `ReleaseDigest.schema=2`、`RunRequest.stages`/`volume`、`ReleasesReport.checked/changed`（必填）。

### Fixed
- `ReleaseDigest.degraded` 与逐条 `summarySource` 的语义对齐：现在由「仓库这一周是否降级」
  决定，`releases.degraded` 计的是降级仓库数（此前按版本条数计）。

## 0.8.0

### Added — 发版单仓分析（contract 0.5.0）
- `run --stages releases --repo <owner/repo>`：只分析指定的单个订阅仓库的 Release，
  不抓榜单、不渲染 digest、不落地订阅 pending 队列（`buildReleasesOptions` 收敛轮询列表）。
  `--repo` 仅在 `--stages releases` 下有效，非该场景或形态非法时回 `CONFIG_INVALID`。
- `RunRequest.command=run` 允许携带 `repo`（需与 `stages` 含 `releases` 搭配）；
  契约升至 0.5.0（wire 整数仍 `ipcSchema=2` / `digestSchema=2`，字段为增量，向后兼容）。

## 0.6.0

### Changed — 订阅数据版本化（方案 A）
- 订阅列表从本机 `subscriptions.yaml`（`.gitignore`）迁到数据仓内受版本控制的
  `<output.dir>/subscriptions.yaml`（默认 `digests/subscriptions.yaml`），随产物同一次
  commit/PR 提交。`run` / `serve` / IPC `subscribe list` 三处经
  `subscription.RepoSubscriptionsPath` 读取同一文件，`serve` 的 digests root 与订阅路径
  统一用 `render.OutputRoot` 归一（此前 serve 按 config 目录解析，与 run 不同源）。
- `subscribe add/remove` 只写 `<repo>/.git/trendradar-subscriptions.pending`（不碰工作区，
  绕开 `run` 的 `IsClean`）；`run` 切到当日分支后把 pending 合并进订阅文件，`commit`+`push`
  都成功后才 `ClearPending`（push 失败下次 run 重放，宁可重复一次 commit 不丢订阅）。
- 提交判定独立于产物：`!artifactsUnchanged || subsDirty`，再由 `HasStagedChanges` 决定是否
  commit——修掉「产物不变但订阅变了却被 `filesMatch` 跳过」的丢提交问题。
- `subscribe --file` 退化为只读排查入口：`list --file` 可读，`add`/`remove --file` 返回
  `CONFIG_INVALID`。`subscribe add/remove/list` 现要求 `--config`（保证与 `run` 同源）。
- 迁移：目标分支无订阅文件时用 pending / 旧本机文件合并导入；两边都有且不同则取并集并 warning。
- `.gitignore` 的 `subscriptions.yaml` 收窄为 `/subscriptions.yaml`（仅根目录），
  数据仓内 `<output.dir>/subscriptions.yaml` 必须入库；`subscriptions.example.yaml` 改为空模板。
- 契约不变（订阅仍是 `string[]`）。

### Added — 发版订阅纳入契约（contract 0.3.0）
- `contract/schema/releases.v2.json`：`ReleaseDigest` / `RepoReleases` /
  `ReleaseItem` 成为契约类型；`internal/releasedigest` 改为契约别名，形状全仓只定义一次。
- `RunRequest.command` 支持 `subscribe`（`action` + `repo`，仍严格校验）；
  `trendradar subscribe --json` 可从 stdin 接 `RunRequest`，供插件调用。
- `RunResult.releases` 改为契约类型（不再借 `additionalProperties` 兜底）。
- `serve` 新增只读接口：`/api/v2/releases`、`/api/v2/releases/{date}`、
  `/api/v2/subscriptions`。
- 成本可见性：`releases.usage` 恒存在，用于区分「未调用」（`calls=0`）与
  「调用了但未上报」（`calls>0 && usageKnown=false`）；旧的 5 个 `omitempty`
  token 字段在全 0 时会被序列化吞掉，已修复。

## 0.5.0

Repo release subscriptions: track new releases of the repositories you care
about and get Chinese changelog summaries in the data repo.

### Added
- `trendradar subscribe add|remove|list` and `internal/subscription`: the
  subscription list lives in its own `subscriptions.yaml` (validated,
  deduplicated, atomically written), so it is never rewritten into
  `config.yaml`. `run` and `subscribe` share one path rule
  (`--file` → `config.releases.subscriptionsFile` → `./subscriptions.yaml`,
  anchored at the config directory).
- `releases` stage in `trendradar run` (config `releases.*`, disabled by
  default): fetches new GitHub releases per subscription (falling back to tags
  only for repos with no releases), summarizes them in Chinese, and renders
  `<output.dir>/releases/<period>/<bucket>/<name>.{md,json}` plus
  `releases/index.md` on the same branch and PR as the trending digest.
- Incremental detection paces through pages until the cursor is crossed;
  `perRepoLimit` overflow is deferred (oldest first) instead of dropped, and
  the first run can record a baseline instead of backfilling history.
- Tag-only repos use the tagged commit's message as the summary basis.
- Same-day runs are union-merged by `repo@id/tag`; unchanged content produces
  no commit, and the cursor is persisted only after a successful publish.
- Observability: `run --json` gains a `releases` block and
  `trendradar-usage.jsonl` gains a `stage="releases"` line.

Optional external-context enrichment (Track B) for more accurate summaries.

### Added
- `internal/enrich`: best-effort external context for pending repos. `repo` mode
  adds GitHub topics; `readme` mode also adds an 800-rune README excerpt. Topics
  come from the repository HTML and README from `raw.githubusercontent.com`, so
  the REST 60/h limit is avoided. Results are cached for 7 days under
  `<repo>/.git/trendradar-enrich-cache.json`; concurrency, per-request timeout and
  a whole-run budget bound the work.
- Per-repository failure isolation: 404 / timeout / 429 are recorded in an
  `enrich` report and degrade to description-only summaries; a rate-limit signal
  stops new requests. `--dry-run` only reads the enrich cache and never hits the
  network.
- Prompt now emits a dedicated `TOPICS=` line and `README_EXCERPT=` line alongside
  the untouched `REPO=/DESC=` first line. README/topics are treated as untrusted
  reference data (cleaned and truncated) and never executed.
- Summary cache namespace includes the enrich mode, so A/B modes and the
  no-context baseline never reuse each other's prose.
- Config: `summarize.enrich` (`none|repo|readme`, default `none`),
  `readmeChars`, `enrichCacheTTLHours`, `enrichConcurrency`,
  `enrichTimeoutSeconds`, `enrichBudgetSeconds`.
- Observability: `run --json` gains optional `stages` and `enrich` fields;
  `trendradar-usage.jsonl` records `durationMs`, `summaryDegraded` and the enrich
  counters. Digests stay `schema:2`; `RepoSnapshot`/`Digest` are unchanged.
- Eval harness: `TRENDRADAR_EVAL_ENRICH=none|repo|readme` re-runs the 25-repo A/B.

### Added — 仓库发版订阅（releases）

- `internal/subscription` + `trendradar subscribe add|remove|list`: 手动维护订阅列表，
  存于独立 `subscriptions.yaml`（默认相对 config 文件目录；`--file` 可覆盖），
  带 `owner/repo` 校验、去重、原子写（0600）；重复 add 幂等，remove 不存在返回非 0。
- `internal/releasewatch`: 每日增量拉取订阅仓库的新 Release（无 Release 时回退 Tag），
  按 `id/publishedAt/seenTags` 推进游标；首次可只记基线（`baselineOnFirstRun`）。
  分页覆盖到越过游标，避免页码边界漏项；`perRepoLimit` 溢出按最旧优先延后到下次。
- 中文发版摘要：专用 prompt、notes 注入防护（`<RELEASE_NOTES>` 定界）、
  缓存与单条失败降级（`summarySource=original`），支持 `includePrerelease/drafts`。
- 渲染 `<output.dir>/releases/<period>/<bucket>/<name>.{md,json}` 与 `releases/index.md`，
  与 trending 同分支/同一次 PR；同日二次运行按 `repo@id/tag` 并集合并，不覆盖丢条目。
- 幂等：`render.Comparable` + contentHash；只有发布成功后回写游标。
- 可观测：`run --json` 增 `releases` 报告块；`trendradar-usage.jsonl` 增 `stage="releases"` 行。

## 0.4.1

Improve summary accuracy and record per-run token usage; support local `.env`
for test secrets.

### Added
- Local secret loading: `trendradar` auto-loads nearest `.env.local` / `.env`
  (or `TRENDRADAR_ENV_FILE`); real env wins. `config.yaml` supports `${VAR}`
  expansion, so API keys stay out of the repo. `.env.example` is committed.
- Summary prompt v2: each request now includes language, stars, stars-in-period
  and rank, with explicit style rules and few-shot examples; default temperature
  is 0.2 when unset. `PromptVersion` is bumped to `v2`.
- Summary output validation/sanitization: Markdown links/images, HTML tags, bare
  URLs and control characters are stripped; only missing/invalid repos are
  re-requested instead of re-billing the whole batch.
- `summarize.batchSize` / `summarize.maxRetries` config knobs.
- Per-run token accounting: summaries record prompt/completion/total (and cached)
  tokens by reading the model response's usage. `run --json` now carries a
  `usage` block (contract `Usage`), and non-dry runs append one line to
  `<repo>/.git/trendradar-usage.jsonl` for later aggregation.
- `usage` also reports `calls` / `retries` / `batches` / `cacheHits`, so retries
  and cache hits are visible rather than silently folded into a single number.
- Contract: `RunResult` gains the optional `usage` block (`Usage` def) with
  matching Go and TypeScript types.

## 0.4.0

Per-period output layout: daily / weekly / monthly trending digests can now
coexist on the same date without overwriting each other.

### Changed (Breaking: daily path moved)
- Artifacts now live at `<out>/<period>/<bucket>/<name>.{md,json}`:
  - daily → `daily/<YYYY-MM>/<YYYY-MM-DD>.{md,json}`
  - weekly → `weekly/<ISO-year>/<YYYY-Wnn>.{md,json}` (ISO week/year, e.g.
    `2026-11-01` → `weekly/2026/2026-W44`)
  - monthly → `monthly/<YYYY>/<YYYY-MM>.{md,json}`
  The old `trending/<date>/<date>.*` layout is gone; the data repo must be
  migrated into `daily/` (one-off `git mv`).
- Branches are now `trending/<period>/<name>` (e.g. `trending/weekly/2026-W44`),
  so the same date's three periods get distinct branches/PRs.
- `index.md` is grouped into daily/weekly/monthly sections and accumulates
  history (daily 90 / weekly 12 / monthly 12) instead of being rewritten to a
  single row each run.

### Added
- `run --period daily|weekly|monthly` overrides `config.trending.since` for both
  the fetch and the stored period.
- `serve`: `GET /api/v2/digests/{period}/{date}` (validated period; invalid → 400)
  and `GET /api/v2/manifest?period=` filtering. Manifest entries gain a `period`
  field. `/feed.xml` stays daily-only.

### Unchanged (compatibility)
- Digests are still v2 (`schema:2`, `contentHashAlgo`) and the contract wire is
  unchanged (`ipcSchema=2` / `digestSchema=2` / `contractVersion=0.2.0`).
- `GET /api/v2/digests/{date}` is preserved and maps to daily; its response is
  byte-identical to before.

## 0.3.2

### Removed (v1 contract retirement)
- Removed the legacy v1 contract snapshot (`contract/legacy-v1/`), the
  `tools/contracthash` gate, the CI `contract` job, and the goreleaser
  `CONTRACT.sha256` extra file.
- `serve`: `/api/v1/digests/{date}` and `/api/v1/manifest` now return **410 Gone**;
  use `/api/v2/*` (and `/feed.xml`).

## 0.3.1

### Changed
- Per-date branches are **append-only**: a rerun reuses the branch and adds a
  commit instead of resetting it from the base. Every push is therefore a plain
  fast-forward; `--force-with-lease` is gone.
- A rejected push is fetched, rebased onto the remote branch and retried once.
- Idempotency is decided from the repository, not a local state file: a rerun
  skips when the branch already holds the same artifacts (`generatedAt` aside).
- Summaries are cached in `.git/trendradar-cache.json` across runs, scoped by
  provider/model/prompt version.
- Git push failures now carry `error.details =
  {stage, reason, branch, remote}`.
- `run --json` reports `commit` and an additive `skipReason`
  (`merged` | `pr-closed` | `unchanged`).

### Added
- `run --trigger=schedule`: a scheduled run respects a human-closed PR (skips
  with `skipReason=pr-closed`); a manual run still rebuilds and reopens it.

### Removed
- `internal/state` (`.git/trendradar-state.json`). The repository is the source
  of truth; the plugin keeps UI history.

## 0.3.0

Adopt the shared contract v2 (`@morehao/trendradar-contract@0.2.0`; wire
`ipcSchema=2` / `digestSchema=2` / `contractVersion=0.2.0`).

### Changed
- `internal/ipc` types now alias the shared contract package (single source).
- `run --json` emits the contract `RunResult` (adds `branch`/`commit`/`prUrl`/
  `contentHash`, `startedAt`/`finishedAt`); the ad-hoc `runOutput` is gone.
- `contentHash` is now RFC 8785 JCS + sha256 over a fixed object (Go/TS agree);
  rendered digests are written as v2 (`schema:2`, `contentHashAlgo`).
- `serve` exposes `/api/v2/digests/{date}` and `/api/v2/manifest`; `/api/v1/*`
  redirects (302) to v2 for one release; `/feed.xml` item links point at v2.
  Historical v1 digests are normalized to v2 on read with
  `contentHashAlgo=legacy-struct-marshal-v1`.
- `state.json` carries `stateSchema:2`; an incompatible v1 state triggers one
  re-run (first run after upgrade re-renders; `contentHash` values change).

### Upgrade
- First run after upgrading re-renders and rewrites digests as v2; expected.
- Pair with plugin `0.3.0` (which consumes this contract) in the same window.
