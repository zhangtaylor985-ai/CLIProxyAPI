# Claude 客户端兼容活跃调查

这份文档只记录“线上仍在发生、还不能宣布完全收口”的问题。

和 [Bug 台账](./bug-registry_CN.md) 的分工是：

- Bug 台账：已确认根因、已有修复提交的历史问题
- 活跃调查：最新线上样本还在出现、需要继续收敛的回归或风险

## ACT-001 `Agent/Explore` 子代理链路仍有活跃回归

- 当前状态：调查中
- 典型症状：
  - `API Error: Failed to parse JSON`
  - `API returned an empty or malformed response (HTTP 200)`
  - `stream disconnected before completion: stream closed before response.completed`
- 最新线上证据：
  - `2026-04-22` 的用户 session `aebc7029-28c2-4c5b-83f9-f368e6ffa566`
  - 在 plan mode + `Agent(subagent_type="Explore")` 链路里，同一会话同时出现：
    - 子代理 `tool_result` 直接落 `Failed to parse JSON`
    - 子代理 `tool_result` 直接落 `empty or malformed response (HTTP 200)`
    - 随后还有 `408` / `stream closed before response.completed`
- 当前判断：
  - 这不是早期已经修过的“首包前错误体 schema 不兼容”那一类老问题
  - 更像是 `Agent` 子代理专属链路在长会话 / 读多文件 / 计划模式 / 重试收敛时，仍有成功流和重试链路的边界问题
- 下一步排查重点：
  - 用真实 `cc1` / `claude2` + `Agent/Explore` 场景做定向黑盒
  - 对齐三份证据：
    - session `jsonl`
    - 客户端 `--debug-file`
    - 服务端 `logs/main.log`
  - 重点盯：
    - 子代理第一次失败时的原始 HTTP 状态和响应体
    - 是否在重试后被 UI 再次合成为 synthetic parse 错
    - `response.completed`、tool-result 回放、subagent result 收口顺序

## ACT-002 上游 TLS / 证书校验失败会被用户感知成通用 API 错误

- 当前状态：风险确认，归因不在本地 translator 本身
- 典型症状：
  - UI 端先看到 `API Error: Failed to parse JSON`
  - session 里真实原因其实是：
    - `UNKNOWN_CERTIFICATE_VERIFICATION_ERROR`
    - 请求目标：`https://cc.claudepool.com/v1/messages?beta=true`
- 最新线上证据：
  - `2026-04-22` 的用户 session `aebc7029-28c2-4c5b-83f9-f368e6ffa566`
  - 在长回合尾部先连续出现 TLS 校验错误，再合成 synthetic `Failed to parse JSON`
- 当前判断：
  - 这类问题不应再误记成“Claude -> GPT translator 又坏了”
  - 它更像是：
    - 上游域名证书链问题
    - 中间代理证书问题
    - 客户端未走预期本地链路，直接打到了上游域名
- 下一步排查重点：
  - 确认客户端实际命中的 `ANTHROPIC_BASE_URL`
  - 对齐 debug-file 里的真实请求地址
  - 如果不是本地代理链路，转入环境/证书排查，不占用 translator 修复口径

## 维护规则

后续只要出现新的线上样本，先决定放哪里：

1. 已能确定根因且已有修复提交：
   - 记到 [Bug 台账](./bug-registry_CN.md)
2. 线上仍在发生，根因还没完全闭环：
   - 先记到这份活跃调查
3. 主要是 hook / plugin / 证书 / 外部 wrapper：
   - 先记为风险，再决定是否升级为代理兼容 bug
