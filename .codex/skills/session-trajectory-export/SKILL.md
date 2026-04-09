---
name: session-trajectory-export
description: 当用户要求导出 CLIProxyAPI 的 session trajectory、会话轨迹、session-data 导出文件，或询问前端“导出当前会话 / 批量导出筛选结果”后文件从哪里获取、如何从生产机拉取时使用。适用于本地仓库 `/Users/taylor/code/tools/CLIProxyAPI-ori` 和生产机 `/home/azureuser/cliapp/CLIProxyAPI` 的轨迹导出、全量导出、时间范围导出、导出路径定位与回传说明。
---

# Session Trajectory Export

## Overview

这个 skill 用于导出 CLIProxyAPI 的会话轨迹文件，并解释管理端 UI 导出后的真实落盘位置与获取方式。

优先复用仓库内原生脚本：

`/Users/taylor/code/tools/CLIProxyAPI-ori/scripts/export_session_trajectories/main.go`

## Workflow

1. 先确认目标环境。
本地仓库默认是 `/Users/taylor/code/tools/CLIProxyAPI-ori`。
生产机默认通过 `ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163` 登录，再进入 `/home/azureuser/cliapp/CLIProxyAPI`。

2. 优先使用原生导出脚本，不直接手写 SQL 导出 JSON。
本地执行：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories
```

按时间范围导出：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories \
  --start-time 2026-04-01T00:00:00Z \
  --end-time 2026-04-05T23:59:59Z
```

仅统计不落文件：

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
go run ./scripts/export_session_trajectories --dry-run
```

3. 生产机导出默认用 `sudo` 执行。
原因：生产上的 `session-data/session-exports` 常见为 `root:root`，普通 `azureuser` 未必可写。

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'cd /home/azureuser/cliapp/CLIProxyAPI && sudo env PATH="$PATH" go run ./scripts/export_session_trajectories'
```

按时间范围导出：

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'cd /home/azureuser/cliapp/CLIProxyAPI && sudo env PATH="$PATH" go run ./scripts/export_session_trajectories \
    --start-time 2026-04-01T00:00:00Z \
    --end-time 2026-04-05T23:59:59Z'
```

4. 导出完成后，优先汇报三类结果。
导出的总 session 数。
导出的总文件数。
manifest 路径与 export root 路径。

5. 如果用户问“前端 UI 点击导出后从哪里获取”。
明确说明当前 UI 只是调用 management API 触发服务端导出，不会把 JSON 下载到浏览器本地。
文件以服务端返回的 `export_dir` 为准；生产默认目录通常是：

`/home/azureuser/cliapp/CLIProxyAPI/session-data/session-exports`

6. 如果用户要求把导出结果拉回本地。
优先给 `scp` 或 `tar` 命令。

单目录拉取：

```bash
scp -i ~/.ssh/cohen.pem -r \
  azureuser@20.240.219.163:/home/azureuser/cliapp/CLIProxyAPI/session-data/session-exports/<dir> \
  /Users/taylor/Downloads/
```

整包压缩后拉取：

```bash
ssh -i ~/.ssh/cohen.pem azureuser@20.240.219.163 \
  'sudo tar -C /home/azureuser/cliapp/CLIProxyAPI/session-data -czf /tmp/session-exports.tgz session-exports'

scp -i ~/.ssh/cohen.pem \
  azureuser@20.240.219.163:/tmp/session-exports.tgz \
  /Users/taylor/Downloads/
```

## Notes

脚本支持 `user-id`、`source`、`call-type`、`status`、`provider`、`canonical-model-family`、`start-time`、`end-time` 过滤。

脚本会输出 manifest JSON，可用于后续前端异步导出任务或离线核对。

当用户进一步要求“批量导出做成异步、可看进度、按时间范围先估算数据量再确认执行”时，先复用当前脚本与现有 management session trajectory 查询链路做方案分析，再决定是否新增后端异步任务接口。
