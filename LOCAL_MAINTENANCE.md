# 本机 Magpie 维护版本

本目录基于官方 `v0.1.2697`，提交
`1747559d02c1e03a6f38dc8506b75e545ee4b89a`。
已部署维护版本为 `0.1.2697-native-cooldown15-retry100`。定制源码维护于
`Cuihongsen/magpie` 的 `codex/local-customizations-v2697` 分支，
官方 `yetone/magpie` 为上游。

初版 `custom-cooldown15` 中，`token-mixing/deepseek-v4.1-flash` 的慢启动和 429 默认
冷却上限为 15 秒；429 明确给出的较长重试或重置时间仍然遵守。
429 的冷却仍沿用 Magpie 原来的密钥级作用范围，可能同时影响该密钥的
其他模型；额度和余额耗尽不使用该 15 秒规则。原生路由的单候选或最后
备用候选仍可在冷却期间尝试，因此这不是严格的请求间隔保证。

2026-11-10 的 `custom-cooldown15` 二进制 SHA-256 为
`85ce807d4a7b503c2a2234b0974dc498d315292cef94f20d5beb6e36b63b536d`。
本机回环假上游验证了 combined-token 恢复，定向路由和 race 测试通过。
完整 `go test -tags nogui ./...` 有下文列出的四项既有失败。

`0.1.2697-crazy-retry20` 在渠道商高级设置和 CLI 增加疯狂重试：
默认关闭，开启后每次请求遇到 429 最多额外重试 20 次，次数可设为
1–100；0 恢复默认值。重试遵循上游 Retry-After，显式等待超过一小时
时转入原有失败处理，取消或已经开始输出时不重放。用本机隔离假上游
验证了设置、重试、耗尽降级、关闭及已有 combined-token 恢复；
定向 race 和界面测试通过。完整 Go 回归仍为下列四项既有失败，
界面导航超时静态测试另有 `account-rows-fit` 和 `window-visibility`
两项既有问题。签名后程序 SHA-256：
`2d15d25fe19eb0552d90bb9589271b0d1a041dfaa4c2800488490fec3b724812`。
2026-11-10 已通过原生 handover 切换，网关运行版本和签名均已核验；
旧 App 已备份，现有渠道默认不启用该功能。

`0.1.2697-crazy-retry100ms` 将疯狂重试的最短间隔从 1 秒改为
100 毫秒，仍遵循上游更长的限流等待。签名后程序 SHA-256 为
`a676c05b3478102ef334cb7c14a45e1e4093e44622509b8f37b3e9ce3e366921`。
本机隔离假上游实测两次重试间隔约 102 毫秒；定向 race 和原有
combined-token 恢复验证通过。

当前版本增加渠道级 `errorCooldownSeconds`，并按照用户最新要求把疯狂
重试固定为 100 毫秒，不再让上游 Retry-After 或重置头延长同次重试。
渠道级恢复冷却与单次请求内重试分开；配置为 15 秒时，上游最终 4xx
或 5xx 后渠道暂停派发 15 秒，其他可用渠道无需等待。原先本机 37491
转发器处理 `no healthy deployments` 的恢复逻辑在网关中实现，并沿用
配置的恢复冷却。取消与已发出的内容不能被重放。

2026-11-11 已将签名后的当前版本安装至 `/Applications/magpie.app`，
二进制 SHA-256 为
`8fa6efbad653eb1d3e891b8b4ea6cf154303b2d15359627088fc663f534d5de2`。
Token Maxing 的渠道配置使用上游直连、15 秒错误恢复冷却，并保留原有
20 次疯狂重试设置；未配置次数时默认 50 次。旧 `37491` 转发器已在
连接排空后停止并禁用，其 launchd 配置和程序保留。旧 App 和渠道配置
备份位于 `~/.local/share/magpie-sub2api/backup-native-cooldown-20261111-190356`。
签名包隔离拨测中，429 后的两次实际发包间隔为 103.045 和 103.086 毫秒，
15 秒冷却及到期恢复、无健康部署恢复和 combined-token 修复均通过。
真实 Token Maxing 的一次短请求在六次 429 后返回 200。四个相关 Go 包的
定向 race、五语言 Node 测试及 Chromium/WebKit 各 15 项测试通过；
未将这些定向结果称为全量测试通过。

## 官方上游合并

2026-11-13 把官方稳定版从 `v0.1.2697` 合并到 `v0.1.3939`。区间共
1755 个提交、4077 个文件变更（+468297/−20500），分两次合并提交：
先 `v0.1.3909`（`d7837f49e`），再 `v0.1.3939`。上游主要更新了
网关兼容（WebSocket、mTLS、Gemini/Vertex、图像/视频、web_search 参数、
更多 OpenAI 兼容端点）、Agent 接入（Codex `-p magpie`、Claude 思考模式、
Hermes、FlowDown、Bob、Mistral Vibe、JetBrains/WSL）、Provider 与账户
（管理 API、Remote Magpie、余额探测、按 key/账户的模型与配额、证书与
掩码策略）、路由与用量（路由模板、服务层级、配额卡、请求用量游标、
速度排行、积分窗口）、MCP/插件/Skill/Library（多账户 MCP、Skill 路由、
Library 目标、插件搜索），以及 Memory、会话导航、备份同步、更新源选择、
构建 attestation 校验和问题报告。

合并时保留全部本地定制，并按上游新 API 做了适配：

- 渠道 `crazyRetry`（默认 50 次、Token Maxing 显式 20 次），同一请求内
  重试间隔固定约 100 毫秒，不再被 `Retry-After` 或重置头延长。
- 渠道 `errorCooldownSeconds`，Token Maxing 为 15 秒；上游最终
  401/其他 4xx/5xx 后渠道冷却，取消或已输出内容不重放。
- combined input/output 上限的本地恢复逻辑，改用上游 `lazyre`。
- Token Maxing 直连 `https://thsmaxtoken.myhexin.com/ai-gateway/v1`；
  旧 `127.0.0.1:37491` 转发器保持停用。
- Codex 的 DeepSeek 仍为 480K 窗口、400K 压缩阈值。

合并后的验证：Gateway 定向 111 项、Provider/CLI/GUI 定向 33 项、
Node/i18n 14 项、`coreScriptBudget` 通过；`node --check` 两个静态资源
通过；`make build` 成功（仅有 CoreML 可用性警告和重复 `-lobjc` 链接
警告）。本轮未运行全量 `go test ./...`，不声称全量通过。

本机 `Go 1.26.8` 工具链位于 `GOMODCACHE` 内，`footprint-overlay.sh`
会因此被 Go 拒绝；构建时使用复制到 `~/sdk/go1.26.8` 的独立
`GOROOT`，即
`GOROOT=$HOME/sdk/go1.26.8 PATH=$HOME/sdk/go1.26.8/bin:$PATH GOTOOLCHAIN=local make build`。

本次只合并源码并推送个人 fork，没有生成新签名 App、没有替换
`/Applications/magpie.app`，已安装版本仍是
`0.1.2697-native-cooldown15-retry100`。分支
`codex-local-customizations-v2697` 已推送到 `Cuihongsen/magpie`，
未在本轮新增 tag。

## 时段路由（原生时间窗）

2026-11-13 把「哪个渠道在什么时段可用」做进网关本身，不再由 launchd
定时改写 `providers.json`。`Group` 新增 `windows`：

```json
"windows": {
  "token-mixing/deepseek-v4.1-flash": {"from": "16:00", "to": "08:00", "zone": "Asia/Shanghai"},
  "ths/deepseek-v4.1-flash": {"from": "22:00", "to": "08:00", "zone": "Asia/Shanghai"}
}
```

值就是规则暂停用的 `TimeWindow`（`from`/`to`/`days`/`zone`），因此跨午夜、
按星期和按时区都与规则一致；不写 `days` 表示每天生效。解析在
`Group.routesAt(now)`，`membersFound` 解析组时按请求时间过滤，窗口外的
成员等同于 `off`；无法读取的窗口视为没有窗口，不会把成员整个摘掉。保存
时 `cleanWindows` 校验时间、星期和时区，并只保留组内成员；成员改名、分享
导入、provider 改名时窗口跟着改；`groupsState` 和保存路径在调用方不带
`windows` 时保留旧值，所以 Routing 页面保存不会抹掉时间窗。

当前配置：THS 4.1 `22:00–次日 08:00`，Token Maxing 4.1 `16:00–次日
08:00`，每天生效（含周末），`08:00–16:00` 两个 4.1 都不可用，第一层降级
到 `ths/deepseek-v4-flash`。22:00–08:00 重叠期仍按 `weights` 的 3:1
（Token Maxing : THS）。

已停用 `local.magpie-ths-v41-window`（`launchctl bootout`），plist 移到
`~/.local/share/magpie-sub2api/disabled-launchd/`，旧
`configure_ths_v41_time_window.py` 不再运行。旧机制的 07:40/08:01 边界和
周末全开随之取消。

本机已安装 `0.1.3939-native-windows`（二进制 SHA-256
`5f953a49dea0d9229aec8adafca41c30160484b44550d56dec4518ce2af4b3d4`），
经原有 handover 接管网关；旧 App 备份在
`~/.local/share/magpie-sub2api/backup-native-windows-20261113-165929`。
首次保存时旧的 GUI 进程仍占着 `127.0.0.1:3430`，把 `windows` 丢了；结束
旧进程后由新版重存成功。验证：provider 全包 2557 项、gateway 组相关
488 项通过；17:02 对 `group/deepseek-v41-speed` 的实际路由候选只有
`token-mixing/deepseek-v4.1-flash`，THS 已被窗口排除。

同日晚些时候把时间窗显示到 Routing 页面：`memberJSON.window` 带
`from`/`to`/`days`/`zone`/`open`，组的卡片在成员名后括注
`(22:00–08:00)`，编辑器成员行显示同一对时间并带悬停说明；当前不在窗口内
时标为 amber。旧值仍只在 `providers.json` 和页面展示，编辑时间窗仍需改
配置（页面只读显示）。已安装 `0.1.3939-window-ui`（SHA-256
`901d52410193b40b6945b91678476087bd252cc6e2a0fb4f40e54637597f393d`），
备份在 `backup-native-windows-20261113-173723`；迁移时旧 GUI 进程占着
3430，最终以单一新实例重启（`open -a /Applications/magpie.app`）。

## GitHub 自动打包

`.github/workflows/fork-package.yml` 在个人 fork 上完成打包：推送到
`codex-local-customizations-v2697` 或推 `v*` tag 时，在 `macos-latest`
上 `make app` 构建 macOS Apple Silicon App、ad-hoc 签名，产出 zip
（tag 另加 dmg）和 `INSTALL.txt`、`SHA256SUMS.txt`，作为 Actions
artifact 保留 30 天；tag 构建还会用 `gh release create/upload` 发布到该
fork 的 Releases。只做 M 系列安装包：不再构建 CLI 或其他平台。没有
Apple 开发者证书，App 未公证，下载后如被 Gatekeeper 拦截需
`xattr -dr com.apple.quarantine /Applications/magpie.app`。

最初一版在 macOS runner 上串行交叉编译 7 个 CLI 目标并重复压缩上传，
一次 16m47s。改为只构建 App、`compression-level: 0` 跳过重复压缩后，
缓存命中时一次 1m48s（约 9.4 倍）；冷缓存主要耗在 Go+cgo 全量编译，
Setup-go 的 module/build cache 会让后续推送走增量。

官方 `release.yml`（移交 `yetone/magpie-releases`）和 `docker.yml`
（推送 `ghcr.io/yetone/magpie`）在 fork 里没有对应密钥与命名空间，已加
`github.repository == 'yetone/magpie'` 守卫；`docker-daily` 和
`docker-stable` 本来就有这个守卫。

## 官方 v0.1.4517 合并

2026-11-19 合并官方 `v0.1.4517`：11 个提交、87 个文件（+4,796/−115）。
重点包括 Codex 工具 `strict` schema 处理（OpenAI/Responses 兼容，修复
LobeHub 一类客户端报 "oneOf constraints are not supported"）；Codex
会话前缀逐请求一致的回归测试；路由组 Ultrafast 档位显示与路由 trace
区分；服务端 SIGTERM 优雅排空在途请求（K8s 滚动更新）；GUI Usage
卡片稳定与暗色 4.5:1 对比度；库 Rules 文件夹与子代理 Markdown 文件；
以及 Pi 市场按 npmmirror 名检索。合并无冲突，本地重试、冷却、时间窗
和 fork 签名更新配置保持不变。

## 官方 v0.1.4508 合并

2026-11-19 合并官方 `v0.1.4508`：5 个提交、49 个文件（+1,413/−131）。
重点包括工具发现支持中文查询且无匹配时列出全部工具；会话恢复到副本
后继续完整读取；CLI 结束/未启动时正确管理插件宿主；容器更新提示显示
找到的具体镜像而非 `:latest`；以及 AGY 调用归入 Usage。合并无冲突，
本地重试、冷却、时间窗和 fork 签名更新配置保持不变。

## 官方 v0.1.4497 合并

2026-11-19 合并官方 `v0.1.4497`：15 个提交、82 个文件（+6,044/−252）。
重点包括修复工具调用后的文本顺序，避免 DeepSeek 误判调用已被回答；
更新重启失败时正确退出；Claude 受限模型路由到有 Claude Code 权限的
账户；Codex 账户额度用尽后切换 Magpie 模型；图片 1K/2K/4K 分辨率设置、
Copilot WebSocket 增量响应和 MCP 网络错误处理等。合并无冲突，本地
重试、冷却、时间窗和 fork 签名更新配置保持不变。

## 官方 v0.1.4483 合并

2026-11-18 合并官方 `v0.1.4483`：18 个提交、123 个文件（+4,056/−435）。
重点包括中继回复头随完成一起到达时的丢失修复、上传重试等待连接关闭、
两个中继调用不再共用回复 ID、更新通知按渠道版本判断、OpenAI Responses
WebSocket 默认只发新增输入、自动图像识别/网页搜索绑定原供应商，以及
OpenCode Go 免费模型输入保持原样。合并无冲突，本地重试、冷却、时间窗
和 fork 签名更新配置保持不变。

## 官方 v0.1.4470 合并

2026-11-18 合并官方 `v0.1.4470`：15 个提交、75 个文件（+5,308/−133）。
重点包括 `ccswitch://` 链接正确交给 CC Switch、Qwen Token Plan 的
TTS/ASR/视频模型走 OpenAI 兼容端点、OpenCode 自动发现 Magpie 模型、
工具搜索无匹配时明确提示、系统消息定位、Codex 模型列表与压缩处理，
以及代码仓库/Skill 卡片统计等。合并无冲突，本地重试、冷却、时间窗
和 fork 签名更新配置保持不变。

## 官方 v0.1.4456 合并

2026-11-18 合并官方 `v0.1.4456`（当时 latest；`v0.1.4457` 只有 tag，
尚未发布）：区间 125 个提交、495 个文件（+23,448/−2,258）。重点包括
`/api/state` 不再返回代理密码、LAN key、Bark key 和 GitHub token；
Slow/Context/Compact 路由组保存修复；分组 Routing/Window 与 provider
设置一致；Claude 工具调用不再 120 秒后转后台；被 Anthropic 停用的
Claude 账户标记受限并让出；滞留流 30 分钟结束；并行会话接近额度上限
时的计数修复；以及 Codex/Claude 的多项兼容性改进。

冲突集中在 `internal/gui/assets/i18n.js`（四种语言的本地重试/冷却
文案与上游新文案并排保留）、`internal/gui/assets/plugins/routing/
client.js`（保留分组时间窗标签，同时接入上游 `memberLine` 排版）和
`internal/gui/assets/plugins/routing/routing.css`（同时保留本地
`.rt-window` 与上游 `.mn` 样式）。网关、provider 和更新器没有冲突，
本地 `crazy_retry.go`、`error_cooldown.go`、`groupwindow.go` 及 fork
签名更新配置均保留。

验证：`make build` 通过；gateway 定制回归 111 项、provider/GUI/CLI
定向 33 项、`internal/update` 240 项、GUI/多语言 Node 测试 14 项通过。
本机上游自带的 Claude account-off 测试在未改动的 `v0.1.4456` 上同样
失败，属于本机测试环境问题，不是本次合入引入；未据此声称全量测试
通过。

## 官方 v0.1.4367 合并

2026-11-17 合并官方 `v0.1.4367`：保留渠道疯狂重试、错误冷却、
分组时间窗、combined-token 修复及 fork 签名更新。上游新增免费模型
分组、DeepSeek 降级、Qwen 并发重试和并行用量限制等功能。
这次合并只更新仓库，不创建 Release 或替换已安装的 App。

## 官方 v0.1.4314 合并

2026-11-16 合并官方 `v0.1.4314`（`187d06724`）：区间 608 个提交、
1705 个文件（+138,758/−8,223）。上游这段时间主要在 Realtime API、
Claude 订阅的 advisor/web search、Remote magpie 的 agent 启动与地址
变更、Codex 配置键校验、Quick check 多搜索 API、侧边栏页面顺序、
Windows 标题栏、按 key 的独立价格、以及大量 provider/agent 兼容性。

冲突与处理：

- `internal/gateway/gateway.go`：保留上游 `provider.Readdressed` 的重试
  地址更新；去掉重复的 `c := cands[i]`；`pauseAlive` 新参数按
  `readdressedOf([]candidate{c})` 传入，疯狂重试和错误冷却仍按 15 秒/
  100ms 逻辑等待。
- `internal/gui/providers.go`、`app.js`：同时保留本地
  `crazyRetry`/`crazyRetryCount`/`errorCooldownSeconds` 与上游新增的
  `keyPriceRates`、`resolve`、`routingAdvanced`；Advanced 展开判断两边
  条件都保留。
- `internal/gui/assets/plugins/routing/client.js`：成员行同时保留本地的
  时间窗标签和上游的整行拖动。
- `internal/gui/assets/i18n.js`：五种语言的本地重试/冷却文案与上游新增
  文案并排保留。
- `core_budget_test.go`：上游预算 1,086,971/1,271,061 加上本地
  2,529 字节的控件，设为 1,089,500/1,273,590。
- `groupwindow_test.go`：上游把 `Entry.Provider` 改成 `*Provider`，测试
  随之取地址。
- `provider-crazy-retry.test.cjs`：沙箱补上 `fieldInputs` 与
  `parentElement`/`querySelector`，因为上游 `field()` 现在自动给输入框
  加 `aria-label`。

验证：`go build -tags nogui ./...`、`make build` 通过；provider 全包
2,688 项、gateway 定向 111 项、GUI 定向 6 项、CLI 定向 7 项、Node
14 项通过。未运行全量 `go test ./...`。时间窗、Token Maxing 配置、
Codex 480K/400K 均未改动。

## 自动更新指向本 fork

`internal/update/fork.go` 增加 fork 更新模式，由构建参数
`-X .../internal/update.forkRepo=Cuihongsen/magpie` 打开；Makefile 的
`FORK_REPO` 默认就是该仓库，所以本 fork 构建的 App 只读自己的 GitHub
Releases：

- 读 `api.github.com/repos/Cuihongsen/magpie/releases/latest`，不再访问
  usemagpie.ai 的官方 feed，也不会把官方更新装进来。
- 安装文件是固定名 `magpie-darwin-arm64.zip`（`update.AppAsset()`），
  hash 取自发布里的 `SHA256SUMS.txt`；CI 现在同时上传这个固定名和带版本
  号的副本。
- Release 必须带 fork 自己的 `SHA256SUMS.txt.sig`：构建内置公钥，
  检查版本与清单绑定的 Ed25519 签名，然后按清单校验下载的 ZIP。
  签名私钥保存在 GitHub Actions secret 和本机受限目录；无签名的旧
  Release 会被新构建拒绝。macOS 包还会校验代码签名。
- Stable/Daily 频道和“What's new”的官方历史 notes 在 fork 模式下不再
  参与；发布页链接改指本 fork。

CI 的 `Fork package` 在 `v*` tag 上会创建 Release 并上传 zip、dmg、
`INSTALL.txt`、`SHA256SUMS.txt` 和签名；先上传到草稿，完整后再公开。
日常推送 `main` 或维护分支也会触发 Apple Silicon 打包。

## 行为

修复位于 Magpie 原有网关：识别 LiteLLM 的 combined input/output
上限错误，根据上游精确输入计数缩减本次输出额度，在同一候选上至多
恢复一次。剩余输出不足或恢复仍失败时使用原生候选筛选和上下文溢出
反馈。备选容量筛选采用错误报告的实际输入量。

不截断提示词，不把某次剩余额度写为全局模型设置，不修改 Codex 配置，
不新增提供商、代理或协议桥。

旧聊天的提供商迁移使用现有 App 会话管理功能，代码未改。
原生窗口的可访问性观察不稳定时，使用 App 设置中的本机浏览器访问；
该页面与网关属于同一 App 进程。

## 回归与构建

```sh
go test ./internal/gateway \
  -run '^Test(CombinedToken|ReplyLeft|DeepSeekReplyFitsTheWindowLeft|AnthropicReplyFitsTheWindowLeft)' -count=1
go test -race ./internal/gateway \
  -run '^Test(CombinedToken|ReplyLeft|DeepSeekReplyFitsTheWindowLeft|AnthropicReplyFitsTheWindowLeft)' -count=1
go test -tags nogui ./...
go vet ./...
GOOS=linux CGO_ENABLED=0 go vet -tags nogui ./...
GOOS=windows CGO_ENABLED=0 go vet ./...
make app VERSION=0.1.2697-native-recovery1
codesign --force --deep --sign - magpie.app
codesign --verify --deep --strict magpie.app
```

使用工作空间相邻目录中的
`magpie-sub2api/verify_combined_token_recovery.py` 对签名后的二进制做
回环假上游验收。`verify_native_live_recovery.py` 会向已有 Token Maxing
账户发送合成长输入，产生实际模型用量；它必须单独、明确运行。
后者只有观测到真实 `400 → room → 200` 才将验收标记为通过。

## 已核实的原版测试问题

完整测试中有四项失败，在未包含本修复的相同版本源码中均复现：

- CLI `TestEveryRouteHasACommandOrIsNamed`：原有路由未登记命令映射。
- Gateway `TestCodexPoolSaysTheClientsVersion`：单跑通过，整包运行时
  前序测试留下更高客户端版本。
- GUI `TestLaneLimitRoutes`：本机正在运行的网关造成环境干扰。
- Provider `TestRenewLoginsKeepsClaudeReading`：原有额度读取断言失败。

这些失败不计为通过，也不混入本次修复。定向回归、race、三个平台的
vet 以及 macOS/Linux/Windows 构建单独记录结果。

## 部署与后续升级

部署前保留原 App，验证候选签名与 SHA-256，通过 Magpie 原有
`/v1/magpie/restart` 交接机制切换已安装二进制，核对实际服务进程版本，
不能只检查磁盘上的 `--version`。

维护版本运行期间，在 App「设置 → 关于」关闭自动更新，避免退出时
安装不包含本修复的官方包。上游更新只有通过上述精确错误回归后才
接受；不要把本目录的测试、补丁或唯一运行产物放在 `/tmp`。
不要通过手改下游配置、会话数据库或另建 Magpie Direct 来迁就更新。

验收计划、可复现测试脚本和证据保存在相邻 `magpie-sub2api` 目录。
