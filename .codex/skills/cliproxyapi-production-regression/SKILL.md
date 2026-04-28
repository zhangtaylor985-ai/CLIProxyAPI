---
name: cliproxyapi-production-regression
description: Use when preparing CLIProxyAPI changes for production, deciding the pre-release regression scope, or answering whether CC1 PTY blackbox regression or Chrome MCP browser testing is required. Applies to `/Users/taylor/code/tools/CLIProxyAPI-ori` and the paired management UI repo.
---

# CLIProxyAPI Production Regression

## Goal

用于当前 CLIProxyAPI 项目的上线前回归决策与执行。

目标是做到三件事：

- 按变更风险选择最小但足够的回归范围
- 明确什么时候必须做 `cc1` / Claude Code PTY 黑盒回归
- 明确什么时候必须用 Chrome MCP 做真实浏览器验证

默认后端仓库：

`/Users/taylor/code/tools/CLIProxyAPI-ori`

默认前端仓库：

`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`

## Standard Regression Flow

### 1. 先判定变更类型

先看本次改动实际触达的边界：

```bash
git status --short
git diff --stat
git diff --check
```

按下面四类归档：

- **Protocol / CLI compatibility**：Claude CLI、Codex、translator、streaming、thinking、tool_use、web search、resume/continue、PTY 展示。
- **Request lifecycle / persistence**：中间件、billing、session trajectory、API key policy、DB store、日志、归档、导出。
- **Management API / UI**：管理端 handler、API key 页面、Session Trajectories 页面、系统配置页、前端服务层。
- **Docs / scripts only**：文档、说明、非运行时脚本、注释。

### 2. 后端基础回归

只要后端 Go 代码有改动，至少跑定向包测试：

```bash
go test ./internal/config ./internal/api/middleware ./internal/api/handlers/management ./internal/apikeyconfig
```

如果触达共享 API、middleware、session trajectory、billing、translator、runtime executor，扩大到：

```bash
go test ./internal/api/... ./internal/sessiontrajectory ./internal/config ./internal/apikeyconfig
```

上线前最终门禁默认跑：

```bash
go test ./...
```

如果 `go test ./...` 失败，要区分：

- 本次改动导致：必须修复后再上线
- 环境依赖或历史 flaky：记录失败包、失败原因、与本次改动是否相关，再决定是否允许继续

### 3. 前端基础回归

只要管理端前端代码有改动，至少跑：

```bash
npm run build
npm run lint
```

`npm run build` 覆盖 TypeScript 和生产打包；`npm run lint` 覆盖常规静态问题。

管理端前端源码默认只认：

`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`

不要把后端暴露的 `/management.html` 当成管理端前端源码或前端发布产物。需要登录、调用管理端 API、做浏览器校验时，认证信息直接从后端仓库 `.env` 读取：

- `MANAGEMENT_PASSWORD`
- `MANAGEMENT_TEST_ADMIN_*`
- `MANAGEMENT_TEST_STAFF_*`

命令里可以 `source /Users/taylor/code/tools/CLIProxyAPI-ori/.env` 后使用变量，但不要在日志、截图或最终回复里输出明文密码、Token 或完整 API Key。

### 4. 配置与持久化检查

涉及 API key policy、billing、session trajectory、management users、PG store 时，确认：

- 新字段是否进入 Go struct、management API view、前端类型和表单 draft
- UI 保存是否走 PG row store，而不是只写 `config.yaml`
- 是否需要 migration；JSON policy 字段通常不需要，新增表/列/索引需要
- `config.example.yaml` 是否需要补注释
- `.env` 中是否已有对应 PG store 变量，只输出 `set/unset`，不要输出密钥

检查示例：

```bash
for k in APIKEY_POLICY_PG_DSN APIKEY_POLICY_PG_SCHEMA PGSTORE_DSN PGSTORE_SCHEMA; do
  if grep -q "^${k}=" .env 2>/dev/null; then echo "$k=set"; else echo "$k=unset"; fi
done
```

## CC1 PTY Blackbox Decision

### 必须做 CC1 / PTY 黑盒回归

满足任一条件就必须做真实 `cc1` / Claude Code PTY 黑盒回归，并使用 `cc1-tty-blackbox-testing` skill：

- 改动触达 Claude CLI 兼容链路：`/v1/messages`、Claude request/response translation、Claude Code handler、client identity、headers、auth relay。
- 改动触达 streaming / SSE / stream-json / partial messages / final body flush。
- 改动触达 `thinking`、tool_use/tool_result、signature 清洗、resume/continue、conversation replay。
- 改动触达 web search 意图识别、web search 可见性、工具进度展示。
- 用户报告的问题只在真实终端、同一 PTY 多轮、长会话或复杂工具调用中复现。
- 要验证“客户端是否真的命中本地 CLIProxyAPI”，或需要对齐 Claude debug log、server log、request log。
- 发布目标明确是推进 `Target CC-Parity` 或修 Claude Code 体验硬错误。

PTY 黑盒回归最小口径：

- 先做 `-p` 非交互 `LOCAL_HIT` 验证
- 再做同一 PTY 连续多轮
- 用 debug-file、server log、session jsonl 下结论，不单靠屏幕输出

### 不需要 CC1 / PTY 黑盒回归

满足以下情况时，默认不做 CC1 / PTY 黑盒回归：

- 变更只影响 post-response persistence，例如 session trajectory 是否记录、导出、归档、查询。
- 变更只影响 management API / UI 配置保存，不改变客户端请求/响应内容。
- 变更只影响 API key policy 的管理字段，且不改变模型路由、认证、请求 body、响应 body。
- 变更只影响 billing 聚合、后台统计、文档、示例配置。
- 单元测试或 handler 测试已经直接覆盖核心分支，且真实 CLI 行为不会观察到差异。

负责人判断原则：

- 如果改动不会改变 Claude CLI 看到的协议、流式事件、终端 UI 或上下文历史，就不要为了“显得完整”强行跑 PTY。
- 如果用户特别要求黑盒，按用户要求执行。

## Chrome MCP Decision

### 必须用 Chrome MCP 做真实浏览器验证

满足任一条件就必须用 Chrome MCP 或等价真实浏览器自动化验证：

- 新增或重排关键 UI 页面、导航、表单、模态框、表格、详情页。
- 改动影响登录、权限角色、API key 创建/编辑/删除、Session Trajectories 查询/导出、配置保存这类关键管理路径。
- 改动涉及响应式布局、复杂 CSS、可视化图表、滚动容器、长文本、状态切换、禁用态、错误态。
- 用户明确要求“实际点一下”“浏览器测试”“截图确认”“Chrome MCP”。
- 构建通过但仍存在高 UI 风险，例如字段可能看不到、按钮可能被遮挡、表单保存路径不清晰。

Chrome MCP 最小口径：

- 启动或连接前端 dev server / 预览服务
- 登录管理端或使用已有会话
- 打开受影响页面
- 执行关键点击/输入/保存流程
- 截图或读取 DOM 状态确认控件可见、状态正确、无明显布局遮挡

### 不需要 Chrome MCP

满足以下情况时，默认不做 Chrome MCP：

- 后端-only 变更，没有 UI surface。
- 前端只改类型定义、API client 字段透传，页面 JSX/CSS 没改。
- UI 改动是小范围复用现有稳定组件，且不影响导航、布局、保存流程；`npm run build` 和 `npm run lint` 已通过。
- 文案或注释变更，不影响交互。
- 当前环境没有可用管理端凭据或服务，且本次风险可由 build/lint/单测覆盖；需在报告里说明未做浏览器验证。

负责人判断原则：

- 改 UI 但只是给既有表单追加一个同类 ToggleSwitch，且保存路径由类型和 API 测试覆盖，可以不跑 Chrome MCP。
- 改页面结构、关键 workflow 或 CSS 布局时，必须跑真实浏览器验证。

## Recommended Matrix

| Change type | Required regression |
| --- | --- |
| Go backend helper / policy field | targeted Go tests + `go test ./...` before release |
| API key policy persisted in PG | management handler tests + apikeyconfig tests + PG store path review |
| Session trajectory write behavior | middleware tests + sessiontrajectory tests + optional DB smoke |
| Management UI small field addition | `npm run build` + `npm run lint`; Chrome MCP optional by risk |
| Management UI layout/workflow change | `npm run build` + `npm run lint` + Chrome MCP |
| Claude CLI protocol/streaming/thinking | targeted Go tests + `go test ./...` + CC1 PTY blackbox |
| Docs/scripts only | syntax/static checks relevant to changed files |

## Production-Ready Report

最终回复必须包含：

- 本次变更范围
- 跑过哪些测试，明确命令和结果
- 是否做了 CC1 PTY 黑盒；如果没做，说明为什么不需要
- 是否做了 Chrome MCP；如果没做，说明为什么不需要
- 是否需要 migration
- 是否已触发部署/重启；如果没有，要明确“代码已达可上线状态，但未发布”

如果 AGENTS 要求任务完成通知，最后执行：

```bash
/usr/bin/osascript -e 'display notification "<任务完成状态>" with title "Codex" sound name "Funk"'
```
