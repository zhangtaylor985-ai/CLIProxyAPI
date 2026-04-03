# 2026-04-02 Claude CLI 生产复核与收敛记录

## 目标

- 只看 `claude cli -> gpt` 链路
- 以 `Target CC-Parity` 为准
- 重点判断当前未提交改动是否适合生产上线

## 本轮完成

1. 复核了当前未提交代码中的三处高风险点
   - `internal/runtime/executor/claude_executor.go`
   - `internal/translator/gptinclaude/gptinclaude.go`
   - `internal/translator/codex/claude/codex_claude_response.go`
2. 对照了本地 Claude CLI 源码
   - `/Users/taylor/sdk/claude-code/query.ts`
   - `/Users/taylor/sdk/claude-code/cli/print.ts`
   - 相关 transport / main 代码
3. 做了真实 Claude CLI 同一 PTY soak
   - 旧样本：`/tmp/claude-pty-soak-20260402-round2.log`
   - 新样本：`/tmp/claude-pty-smoke-after-fix-20260402.log`
   - 补充样本：`tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-long-session-20260402.out`
   - 搜索后继续追问样本：`tasks/blackbox-samples/2026-04-02-claude-cli-pty/claude-pty-search-followup-20260402.out`
4. 做了两项生产向收敛
   - 收窄 `thinking` replay 保留范围
   - 移除 Claude CLI assistant 文本型工具进度注入
5. 完成验证
   - `go test ./...`

## 本轮新增黑盒结论

### 改动后同一 PTY 长会话

- 已补一组改动后的同一 PTY 长会话样本
- 样本包含：
  - 本地代码追问
  - 中文 `不要联网 / 不要 web search` 负样本
  - 中文 `请帮我搜索一下` 正样本
- 结果：
  - 未见 `invalid signature`
  - 未见 `502`
  - 第 1 轮出现过一次 `Grep` 参数错误，但 Claude CLI 自行恢复并完成回答

### 中文 web search 边界

- 负样本：
  - `请只查看当前仓库...不要联网，不要 web search`
  - 真实样本只做本地 `Grep/Read`，未触发 `Web Search(...)`
- 正样本：
  - `请帮我搜索一下 OpenAI Codex ...`
  - 真实样本明确触发 `Web Search(...)`
- 搜索后继续追问：
  - 在独立同一 PTY 样本中，搜索回答返回 `https://developers.openai.com/codex`
  - 随后继续追问“复述链接域名”成功回答 `developers.openai.com`

### 生产判断更新

- 现阶段不建议继续热修中文 web search matcher
- 原因：
  - 真实负样本没有误触发
  - 真实正样本可以正常触发
  - 继续改更容易把“能搜到”改成“搜不到”
- 当前更像需要继续收敛的是：
  - 搜索回合长尾
  - 搜索过程可见性
  - queued message 体验

## 本轮判断

### 可以收敛并保留

- `sanitizeThinkingHistory` 的方向是对的，但必须按最新 `tool_result` trajectory 收窄，而不是保留所有旧的 tool_result 邻接 assistant

### 不应按当前形态上线

- Claude CLI assistant 文本型工具进度注入
  - 副作用大于收益
  - 会污染 transcript

## 输出文档

- `cc-cli-analysis/2026-04-02-claude-cli-source-soak-production-review_CN.md`
- `AGENTS.md`

## 当前状态

- 比本轮开始时更接近生产稳定
- 但仍未达到可以写 `releases/` 的程度
