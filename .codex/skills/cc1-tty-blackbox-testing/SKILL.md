---
name: cc1-tty-blackbox-testing
description: 当用户要求对 Claude Code / `cc1` / `claude2` 做真实 TTY 或 PTY 黑盒测试，尤其是要验证本地 `ANTHROPIC_BASE_URL` 是否真的命中 CLIProxyAPI、抓取 request log、对齐客户端 debug log 与服务端日志、排查“看起来没打到本地”这类假阴性时使用。适用于当前仓库 `/Users/taylor/code/tools/CLIProxyAPI-ori`。
---

# CC1 TTY Blackbox Testing

## Overview

这个 skill 用于当前仓库的 Claude Code 真实终端黑盒测试。

目标只有四个：
- 先确认客户端到底用的是哪个可执行文件和哪个配置目录
- 再确认 `ANTHROPIC_BASE_URL` 是否真的生效
- 用客户端 `--debug-file` 与服务端日志对齐真实请求
- 避免把“终端没显示出来”误判成“客户端没打到本地”

当前仓库里，这类测试的默认目标是：

- 本地 CLI：`~/.local/bin/claude2` 或 `~/.cac/bin/claude`
- 本地配置目录：`~/.claude_local`
- 本地代理服务：通常是 `http://127.0.0.1:53841`

## When to Use

在这些场景触发：

- 用户要求做 `cc1` / `claude2` / Claude Code 的真实 TTY 或 PTY 黑盒测试
- 用户说“UI 上报错了，但本地 request log 没看到”
- 用户要求确认客户端是否真的命中本地 CLIProxyAPI，而不是外部 Anthropic
- 需要对齐客户端 debug 日志、服务端 request log、session jsonl 三者证据链

不要在这些场景使用：

- 只是跑普通单测或集成测试
- 只是检查某个 handler 的本地单文件逻辑
- 只是看 session jsonl，不需要真正启动本地 Claude 客户端

## Core Pitfalls

### 1. `cc1` 不一定在当前 shell 的 `PATH`

不要默认 `cc1` 可直接执行。

先确认真实入口：

```bash
which claude || true
which claude2 || true
command -v claude || true
command -v claude2 || true
ls -la ~/.local/bin | sed -n '1,40p'
```

在这台机器上，常见真实入口是：

- `~/.local/bin/claude2`
- `~/.cac/bin/claude`

### 2. 终端 PTY 输出不是可信证据

TTY/PTY 下的 ANSI 控制字符很多，工具侧经常抓不到完整 UI。

不要把这些现象当成“客户端没请求”：

- 终端里只看到半截 UI
- tool 抓到的 PTY 输出很脏
- 看不到完整 assistant 正文

真实证据优先级：

1. 客户端 `--debug-file`
2. 服务端 `logs/main.log`
3. request log 文件
4. 最后才是 PTY 屏幕文本

补充说明：

- PTY 屏幕是有价值的，可以用来观察用户实际看到的 UI 现象
- 但它不适合单独判断“请求是否真的发出”“这一轮是否真的提交成功”
- 原因是屏幕上看见输入框里有文字，不等于客户端已经把它落成真实 `user_prompt`
- 另外 ANSI 重绘、工具层截断、滚屏复写都会让“屏幕看起来像没动静”或“像已经提交”这两种判断都出现假阳性

实践规则：

- 看屏幕，用来理解现象
- 下结论，用 `session jsonl + --debug-file + server log`

### 3. 不能只盯 `/v1/messages` 的某一个时间窗

Claude 客户端常见会额外打这些请求：

- `HEAD /`
- `POST /v1/messages` for `generate_session_title`
- `POST /v1/messages` for real `sdk`
- 插件 marketplace 拉取
- 其他预热/探测请求

所以如果你只搜一小段日志，很容易误判“没打到本地”。

### 4. 客户端可能已经从 `settings.json` 注入了 `ANTHROPIC_BASE_URL`

先看：

```bash
cat ~/.claude_local/settings.json
cat ~/.claude_local/settings.local.json
```

如果里面已经有：

```json
"ANTHROPIC_BASE_URL": "http://127.0.0.1:53841"
```

那么再额外猜测“是不是没走本地”意义不大，先看 debug-file。

## Standard Workflow

### Step 1. 确认客户端配置

```bash
cat ~/.claude_local/settings.json
cat ~/.claude_local/settings.local.json
```

重点看：

- `ANTHROPIC_BASE_URL`
- `ANTHROPIC_AUTH_TOKEN`
- `model`

### Step 2. 确认本地代理服务真的在监听

```bash
lsof -nP -iTCP:53841 -sTCP:LISTEN || true
```

如果没监听，再启动本地服务。

### Step 3. 最小非交互验证

先不要急着跑 TTY。

先用 `-p` 做一条最小请求，并强制落 debug-file：

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
~/.local/bin/claude2 \
  --debug-file /tmp/claude-local-debug.log \
  --dangerously-skip-permissions \
  -p 'Reply with exactly LOCAL_HIT'
```

然后检查：

```bash
rg -n "ANTHROPIC_BASE_URL|\\[API REQUEST\\]|/v1/messages|Stream started" /tmp/claude-local-debug.log -S
rg -n "/v1/messages\\?beta=true| 408 | 500 " logs/main.log -S | tail -n 40
```

这一步通过后，才能继续 TTY。

### Step 4. 再跑真实 TTY

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
~/.local/bin/claude2 \
  --debug-file /tmp/claude-tty-debug.log \
  --dangerously-skip-permissions \
  --model gpt-5.4
```

然后在同一 PTY 内做连续多轮交互。

注意：

- PTY 屏幕输出只做参考
- 真正的对齐仍然看 debug-file 和服务端日志
- 同一 PTY 注入下一轮 prompt 前，先确认上一轮已经真正结束
- 最稳妥的结束信号不是屏幕静止，而是 session jsonl 已追加上一轮的 `system` 记录且 `subtype="turn_duration"`

推荐做法：

1. 先把 prompt 文本写入 PTY
2. 再单独补一个 `\r`
3. 继续观察 session jsonl 是否真的新增 `type=user`

不要依赖这些假信号：

- 屏幕上已经看到了你输入的 prompt
- 光标回到了输入框
- UI 似乎没有继续滚动

这些都不等于该轮已经真正提交。

### Step 5. 对齐证据

客户端侧看：

```bash
rg -n "ANTHROPIC_BASE_URL|\\[API REQUEST\\]|/v1/messages|Stream started" /tmp/claude-tty-debug.log -S
```

服务端看：

```bash
rg -n "/v1/messages\\?beta=true| 408 | 500 " logs/main.log -S | tail -n 80
```

如果需要更精确的原始请求样本，再看 request log 目录。

## Known Good Signals

出现这些信号，说明“客户端确实命中本地代理”：

- debug-file 里出现：
  - `settingsEnv keys: ... ANTHROPIC_BASE_URL`
  - `ANTHROPIC_BASE_URL=http://127.0.0.1:53841`
  - `[API REQUEST] /v1/messages`
- 服务端 `logs/main.log` 出现：
  - `POST "/v1/messages?beta=true"`
  - 来自 `127.0.0.1`

## Known Noise

这些不代表主链路失败：

- `HEAD /` 的 404
- marketplace / plugin 自动拉取
- `generate_session_title` 的额外 `/v1/messages`
- codex refresh / oauth refresh 噪声
- billing usage persist timeout 噪声

## Reporting Format

汇报黑盒结果时至少包含：

- 实际客户端二进制路径
- 实际配置目录
- `ANTHROPIC_BASE_URL` 是否生效
- debug-file 里是否看到 `/v1/messages`
- 服务端是否看到 `/v1/messages?beta=true`
- 本轮请求对应的状态码和 request id
- 若 UI 与日志不一致，哪一侧是 source of truth

## One-Line Rule

PTY 屏幕可以看，但不能单独相信；`user_prompt` 是否真的发出，以 `session jsonl`、`--debug-file`、`logs/main.log` 三方对齐为准。
做 `cc1` / `claude2` 黑盒时：

**先用 `-p + --debug-file` 证明请求真的打到本地，再升级到 TTY；不要直接拿 PTY 屏幕输出判断链路成败。**
