---
name: cliproxyapi-systemd-deploy
description: Use when the user asks to pull the latest CLIProxyAPI code, rebuild the production binary, restart `cliproxyapi.service`, verify the running revision, or document the current systemd deployment wiring for the `/root/cliapp/CLIProxyAPI` instance.
---

# CLIProxyAPI Systemd Deploy

## Overview

这个 skill 用于当前仓库的线上拉取与发布。

目标只有四个：
- 在推送前先确认本地与 `origin/main` 的关系
- 在拉取前保护现场，不覆盖并行改动
- 重新编译 `bin/cliproxyapi` 后再重启 `cliproxyapi.service`
- 用 `systemctl`、`journalctl` 和本地 HTTP 探测确认新版本真的生效

当前默认线上实例：

`/root/cliapp/CLIProxyAPI`

当前已确认 unit 名称：

`cliproxyapi.service`

## When to Use

在这些场景触发：

- 用户要求 `git pull`、重新部署、上线、重启当前服务
- 用户要求确认当前 systemd unit 的真实工作目录、环境文件或启动命令
- 用户要求编译当前仓库并让线上实例生效
- 用户要求提交并 push 与部署相关的变更前，先核对远端 `main`

不要在这些场景使用：

- 只是改代码但不涉及发布
- 只是查看某个业务接口或数据库数据
- 目标机器不是当前这台 `/root/cliapp/CLIProxyAPI` 实例

## Safety Rules

- push 到 `main` 前，必须先执行 `git fetch origin main` 并确认是否落后
- 若工作树不干净，先用 `git stash push -u` 保护现场，再做 `pull` / `rebase`
- 默认只接受 fast-forward 拉取；若出现分叉，先停下来说明原因
- 代码有变更要上线时，不能只重启服务，必须先重编译 `bin/cliproxyapi`
- 文档中的部署路径若和线上实际不一致，以 `systemctl cat cliproxyapi.service` 为准
- 上线确认至少包含：运行中的 PID、日志里的 commit/build 时间、以及一次本地 HTTP 探测

## Standard Workflow

1. 先看本地 git 现场。

```bash
git status --short --branch
git fetch origin main
git status --short --branch
```

2. 若有未提交或未跟踪改动，先保护现场。

```bash
git stash push -u -m "pre-deploy-$(date -u +%Y%m%dT%H%M%SZ)"
```

3. 仅在可 fast-forward 时拉取。

```bash
git pull --ff-only origin main
```

4. 重新编译当前仓库二进制。

```bash
VERSION=$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" \
  -o ./bin/cliproxyapi ./cmd/server
```

5. 重启并检查服务状态。

```bash
sudo systemctl restart cliproxyapi.service && sleep 2
systemctl status cliproxyapi.service --no-pager -l
journalctl -u cliproxyapi.service -n 50 --no-pager -l
```

6. 做本地 HTTP 探测。

```bash
curl -fsS http://127.0.0.1:8317/
```

7. 若本次还包含提交与 push：

```bash
git add <relevant-files>
git commit -m "<message>"
git fetch origin main
git rebase origin/main
git push origin main
```

## What to Report

回报上线结果时至少包含：

- 当前分支与远端是否同步
- 实际上线 commit
- 二进制是否已重编译
- `cliproxyapi.service` 是否 `active (running)`
- 日志中看到的 `Version` / `Commit` / `BuiltAt`
- 本地探测是否成功

如果这次没有真正重启服务，要明确说明停在哪一步。

## References

需要精确命令时，继续看：

`references/commands.md`
