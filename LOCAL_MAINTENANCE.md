# 本机 Magpie 维护版本

本目录基于官方 `v0.1.2697`，提交
`1747559d02c1e03a6f38dc8506b75e545ee4b89a`。
已部署维护版本为 `0.1.2697-custom-cooldown15`。定制源码维护于
`Cuihongsen/magpie` 的 `codex/local-customizations-v2697` 分支，
官方 `yetone/magpie` 为上游。

`token-mixing/deepseek-v4.1-flash` 的慢启动和 429 默认
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
