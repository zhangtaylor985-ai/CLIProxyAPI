# Claude 客户端兼容调试 Runbook

这份 runbook 的目标是：以后再遇到 `Claude Code / cc1 / claude2` 的兼容问题时，我们不需要重新摸索一遍。

## 一、先分清楚我们在排查什么

最容易误判的是把所有问题都叫成“parse JSON”。

实际排查时，先把问题归到下面 4 类之一：

1. 错误体问题
   - 典型表现：`API Error: Failed to parse JSON`
   - 但服务端其实返回了一个合法 JSON，只是 schema 不对
2. 成功流协议问题
   - 典型表现：工具执行完了，后续继续写正文时炸掉
   - 常见和 `response.completed`、`output_item.done`、tool block 收尾有关
3. 请求翻译问题
   - 典型表现：某类 `tool_result`、结构化输出、非标准内容块进入上游后丢信息
4. 环境/插件噪音
   - 典型表现：hook 输出混杂、`claude-mem` 尾延迟、MCP 鉴权报错
5. 上游 / TLS / 证书问题
   - 典型表现：UI 最后显示通用 API 错，但 session 里真实原因是 `UNKNOWN_CERTIFICATE_VERIFICATION_ERROR`
   - 这类问题先确认实际命中的请求地址，不要先怀疑 translator

## 二、证据优先级

排查顺序固定如下：

1. `session jsonl`
2. 客户端 `--debug-file`
3. 服务端 `logs/main.log`
4. request log / 特定请求样本
5. 最后才是 PTY 屏幕内容

### 为什么不能只看 PTY 屏幕

因为：

- ANSI 重绘会让你误以为“已经提交”
- 输入框里看到 prompt，不等于 `user` 请求真的发出
- 屏幕像卡住，不等于服务端没收到请求

PTＹ 屏幕只能帮助理解“用户看到什么”，不能单独判断“请求是不是发出去了”。

## 三、标准排查步骤

### Step 1. 收集用户证据

至少要拿到其中两类：

- 截图
- `session jsonl`
- 报错时间点
- 是否是 `cc1` / `claude2` / `~/.cac/bin/claude`
- 是否启用了插件 / hook / 自定义 wrapper

### Step 2. 确认真实客户端入口

先确认 `cc1` 到底是什么：

```bash
zsh -ic 'alias cc1 2>/dev/null || true'
which claude || true
which claude2 || true
```

本机当前事实是：

```bash
cc1='CLAUDE_CONFIG_DIR=~/.claude_local claude2 --dangerously-skip-permissions'
```

所以以后做黑盒时，不要再默认 `cc1` 是独立二进制。

### Step 3. 确认配置和本地代理

先看：

```bash
cat ~/.claude_local/settings.json
cat ~/.claude_local/settings.local.json
lsof -nP -iTCP:53841 -sTCP:LISTEN || true
```

至少确认：

- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_AUTH_TOKEN`
- 本地代理端口是否在监听

### Step 4. 先做最小非交互验证

不要一上来就做复杂 PTY。

先做一条最小请求：

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
/Users/taylor/.cac/bin/claude \
  --debug-file /tmp/claude-local-debug.log \
  --dangerously-skip-permissions \
  -p 'Reply with exactly LOCAL_HIT'
```

然后看：

```bash
rg -n "ANTHROPIC_BASE_URL|\\[API REQUEST\\]|/v1/messages|Stream started" /tmp/claude-local-debug.log -S
rg -n "/v1/messages\\?beta=true| 408 | 500 " logs/main.log -S | tail -n 80
```

### Step 5. 再做真实同 PTY 黑盒

这是主验收路径。

标准做法：

1. 起真实 `cc1`
2. 在同一 PTY 内做连续多轮
3. 至少覆盖：
   - 简单 smoke
   - 继续追问
   - 工具调用
   - 工具返回后继续总结

当前我们已经把这套经验固化成：

- [`.codex/skills/cc1-tty-blackbox-testing/SKILL.md`](/Users/taylor/code/tools/CLIProxyAPI-ori/.codex/skills/cc1-tty-blackbox-testing/SKILL.md)

### Step 6. 对齐三份证据

排查时必须对齐：

- `session jsonl`
- debug-file
- `logs/main.log`

最常见的三种误判分别是：

#### 误判 1：屏幕里有 prompt，说明这轮发出去了

错。

真正标准是：

- debug-file 里有 `Hook UserPromptSubmit`
- debug-file 里有 `/v1/messages`
- `session jsonl` 里有新的 `user`

#### 误判 2：服务端返回 200，说明没有协议错误

错。

成功流同样可能出错，比如：

- `response.completed` 缺失
- `output_item.done` fallback 缺失
- tool block 关闭时机错误

#### 误判 3：`Failed to parse JSON` 就一定是坏 JSON

错。

很多时候 JSON 本身合法，但：

- 错误体 schema 不兼容
- SSE 事件组帧不完整
- 成功流消息形状不符合 Claude 客户端预期
- 上游 TLS / 证书错误在 UI 层被收敛成了通用 API 错

#### 误判 4：看到 `Failed to parse JSON`，就能证明是本地代理兼容问题

错。

先确认这轮真实打到了哪里：

- debug-file 里的 `ANTHROPIC_BASE_URL`
- debug-file / session 里的真实请求目标
- 是否出现 `UNKNOWN_CERTIFICATE_VERIFICATION_ERROR`

如果请求目标已经变成外部域名，或 session 明确落了 TLS 校验错误，就应该转去查环境/证书链，而不是继续在 translator 里盲修。

## 四、当前推荐的复现路径

### 路径 A：最小 `-p` smoke

目标：

- 先确认是否命中本地代理

### 路径 B：真实同 PTY 多轮

目标：

- 验证主干兼容链路

建议至少覆盖：

1. `Reply with exactly ...`
2. `What was my previous instruction? ...`
3. 一个会触发 `Bash/Read/Grep` 的任务
4. 紧跟一轮“继续/总结”

### 路径 D：`Agent/Explore` 子代理专项

目标：

- 验证最新线上样本里仍未完全收口的子代理链路

建议覆盖：

1. 进入 plan mode
2. 触发至少一次 `Agent(subagent_type="Explore")`
3. 让子代理执行只读探索
4. 观察 `tool_result` 是否出现：
   - `Failed to parse JSON`
   - `empty or malformed response (HTTP 200)`
   - `stream closed before response.completed`

### 路径 C：每日自动回归

目标：

- 用最稳定的真实 `cc1` 场景做日常健康检查

对应实现见：

- [每日回归任务](./daily-regression-task_CN.md)

## 五、本轮对话总结出来的关键经验

### 1. 症状要和根因分开记

`Failed to parse JSON` 只是 symptom bucket。

如果不拆根因，问题会永远“看起来像没修完”。

### 2. 真实客户端回归必须保留

单测必要，但不够。

这轮好几次都是：

- 单测全绿
- 某条真实 `cc1` 路径仍然会炸

### 3. request translator 和 response translator 要一起看

这轮最后一批问题恰好就是双侧都有坑：

- response 侧缺 `output_item.done message fallback`
- request 侧会丢未知 `tool_result.content[]`

### 4. 每修一条 bug，都要追加一条更窄的回归

不要只加“泛化回归”。

应该直接把线上证据变成最小用例，例如：

- 多 tool_use 同轮后继续生成
- `output_item.done.item.type=message`
- 非标准 `tool_result.content[]`
