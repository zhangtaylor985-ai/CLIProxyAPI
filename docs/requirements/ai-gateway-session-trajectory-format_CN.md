# 会话轨迹存储与导出格式需求

## 1. 背景

我们需要定义一个中间轨迹格式，用来统一不同供应商产出的原始请求/响应日志，便于后续做清洗、回放、分析与跨供应商兼容。

本设计的目标是：

- 由我们自己的服务端在请求进入时直接记录请求与响应
- 在自有 Postgres 中维护会话与单次交互
- 按统一格式导出会话级轨迹文件，便于后续清洗、回放、分析与跨供应商兼容

## 2. 目标

- 记录用户与上层 AI 的完整对话轨迹。
- 以 `session_id` 识别和归并一个完整会话。
- 以“单次模型交互”为粒度保存文件，方便后续清洗与重放。
- 保留核心字段 `system`、`tools`、`messages`、`response`，尽量贴近上游原始结构。
- 对供应商缺失字段允许补充推导字段，但推导规则必须可解释。

## 2.1 用户侧约束

- 默认用户是普通终端用户，不应要求用户为会话轨迹功能额外配置环境变量、header、wrapper 参数或自定义启动方式。
- 对 `cc1` / `claude` 的生产方案，不能依赖用户手工设置诸如额外 metadata 注入、额外 session 标识透传之类的配合动作。
- 生产主目标优先保证“同一 TTY 内连续多轮交互”的可记录与可归并。
- `-p` / `-c` / `--resume` 这类非交互恢复链路应尽量支持，但在未观察到稳定上游会话线索前，不应以牺牲主路径正确性为代价做激进猜测归并。

## 3. 目录规范

建议输出目录：

```text
session-data/
  session-exports/
    [user_id]_[session_id]/
      [index]_[request_id].json
```

补充规则：

- 一个目录对应一个完整会话。
- 目录名优先使用 `[user_id]_[session_id]`。
- 如果 `user_id` 缺失、已脱敏到不可区分、或原值中已混入 `session_id` 导致目录名过长，则退化为仅用 `[session_id]`。
- 一个目录下多个 JSON 文件，每个 JSON 对应一次模型交互。
- 文件按时间顺序排序。
- 文件名优先 `[index]_[request_id].json`。
- 如果没有稳定 `request_id`，退化为 `[index].json`。

## 4. 单次交互 JSON 规范

单文件核心字段如下：

```json
{
  "request_id": "e9d3dde0-884c-4737-be0f-780601212e0a",
  "session_id": "240fcd18-61b1-4e9e-92eb-51eb40c085c3",
  "user_id": "user_xxx",
  "start_time": "2026-03-12T19:04:49.443695Z",
  "end_time": "2026-03-12T19:04:59.113766Z",
  "user_agent": "claude-cli/2.1.74 (external, cli)",
  "call_type": "anthropic_messages",
  "status": "success",
  "provider": "anthropic",
  "model": "glm-4.7",
  "system": [
    {
      "type": "text",
      "text": "..."
    }
  ],
  "tools": [
    {
      "name": "exec_command",
      "description": "...",
      "input_schema": {
        "type": "object",
        "properties": {
          "cmd": {
            "type": "string"
          }
        },
        "required": [
          "cmd"
        ]
      }
    }
  ],
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "修复这个 bug"
        }
      ]
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "tool_use",
          "id": "tooluse_yep0cqHbfL",
          "name": "exec_command",
          "input": {
            "cmd": "ls"
          }
        }
      ]
    }
  ],
  "response": {
    "model": "glm-4.7",
    "session_id": "240fcd18-61b1-4e9e-92eb-51eb40c085c3",
    "choices": [
      "..."
    ],
    "usage": {
      "...": "..."
    }
  }
}
```

说明：

- `system`、`tools`、`messages`、`response` 是核心保留字段，尽量直接承接上游原始结构。
- 其他字段如 `provider`、`model`、`status`、`user_agent`、`call_type`、`duration_ms`、`upstream_log_id` 可按供应商情况补充。
- 如果供应商没有 `start_time` / `end_time`，允许从日志时间与耗时推导。
- `response` 建议保留原始响应主体，避免后续清洗阶段丢信息。

## 5. `session_id` 识别规则

优先级从高到低如下：

1. 请求体或响应体里显式存在的 `session_id`
2. 请求 `metadata.session_id`
3. 请求 `metadata.user_id` 等复合字段中可稳定解析出的 `session_<id>`
4. 运行时仍无法识别时，由服务端生成 canonical `session_id`
5. 离线导出且无数据库上下文时，才允许临时回退为 `unknown-session`

要求：

- 同一 `session_id` 必须落入同一目录。
- 同一目录内的文件必须按会话内实际时间顺序编号。
- 后续清洗逻辑默认以 `session_id` 作为识别“完整对话”的主键。

## 6. 设计结论

### 6.1 会话主键不依赖单一上游字段

本方案不假设所有客户端或供应商都会稳定传入可复用的 `session_id`。

原因：

- Claude CLI、OpenAI 兼容接口、内部代理链路、后续可能接入的其他供应商，其会话语义并不统一。
- 某些客户端即使内部存在 session 概念，也未必会把该字段透传到服务端请求体。
- 多轮对话在很多 API 中本质上是“客户端重发完整历史消息”，而不是“服务端靠会话 ID 恢复上下文”。

因此，系统需要同时维护两类会话标识：

- `session_id`
  - 服务端 canonical 会话主键。
  - 由我们自己的后台生成并长期稳定使用。
- `provider_session_id`
  - 上游显式提供或可稳定解析出的供应商会话标识。
  - 仅作为辅助手段，不作为唯一主键。

### 6.2 请求主键与会话主键分离

- `request_id`
  - 表示单次模型交互。
  - 允许直接取客户端显式请求 ID，或由服务端生成。
- `session_id`
  - 表示多轮会话。
  - 一次会话包含多次 `request_id`。

### 6.3 本阶段存储结论

本阶段采用现有 `PG-first` 架构，新增会话轨迹表落到 Postgres。

原因：

- 当前项目已经将 billing、usage、API key config、group 等核心状态迁移到 PG。
- 会话查询天然需要按用户、时间、模型、状态、请求顺序检索，适合 PG。
- 当前用户规模下，无需引入 MongoDB、DynamoDB 或外部消息队列作为主依赖。

本阶段同时保留文件导出能力：

- PG 是运行时主存储。
- `session-data/session-exports/...` 是导出视图，不是唯一真相源。

## 7. 会话标识规则

### 7.1 `session_id` 类型

新增以下字段语义：

- `session_id`
  - 服务端 canonical session ID，UUID。
- `provider_session_id`
  - 供应商或客户端透传的 session 标识，允许为空。
- `request_id`
  - 单次请求 ID，优先保留客户端原值。
- `provider_request_id`
  - 上游 request ID、响应里的 request ID、外部日志系统 ID 等。

### 7.2 `provider_session_id` 提取优先级

从高到低如下：

1. 请求体或响应体顶层显式 `session_id`
2. 请求 `metadata.session_id`
3. 请求 `metadata.user_id` 等复合字段中可稳定解析出的 `session_<id>`
4. 上游供应商特定字段，例如 `request.sessionId`
5. 无法识别则留空，不再使用 `unknown-session` 作为运行时主键

说明：

- `unknown-session` 只适合离线导出临时占位，不适合作为数据库主键。
- 运行时若无法识别 `provider_session_id`，必须生成自己的 canonical `session_id`。

### 7.3 canonical `session_id` 生成规则

以下情况直接新建 canonical `session_id`：

- 当前请求带显式 `session_id`，但本地不存在对应 canonical session 映射。
- 当前请求无法与任何既有会话稳定归并。
- 当前请求虽然来自同一 `user_id`，但消息历史不连续。
- 距离该用户最近一次会话已超过会话归并窗口。

以下情况复用已有 canonical `session_id`：

- `provider_session_id` 已映射到既有 canonical session。
- 或者命中“消息连续性归并规则”。

## 8. Claude CLI 与无显式会话 ID 场景的归并规则

### 8.1 背景

对于 Claude CLI 一类客户端，服务端经常只能看到：

- `api_key`
- `user_agent`
- `model`
- `messages`
- `system`
- `tools`

这类请求未必稳定带显式 `session_id`，但多轮对话通常会把之前的消息历史继续带回。

### 8.2 归并输入

每次请求进入时，先归一化以下内容：

- `user_id`
  - 由入站 API key 或 API key policy 映射得到。
- `source`
  - 例如 `claude-cli`、`claude-vscode`、`openai-sdk`。
- `call_type`
  - 例如 `anthropic_messages`、`openai_responses`。
- `user_agent`
- `system`
- `tools`
- `messages`

### 8.3 归并窗口

默认仅在同一用户最近活跃会话中尝试归并。

建议窗口：

- 活跃归并窗口：`24h`
- 强连续归并优先窗口：`2h`

### 8.4 消息连续性归并规则

对同一 `user_id` 下最近活跃的候选会话，按以下顺序匹配：

1. 当前请求的 `provider_session_id` 已绑定到某个 session
2. 当前请求的 `messages` 与候选会话最后一次请求的 `messages` 完全相同
3. 当前请求的 `messages` 以前缀方式包含候选会话最后一次请求的完整 `messages`
4. 当前请求比上次仅追加了新的 user message、tool result 或 assistant message

只有同时满足以下条件才允许归并：

- `user_id` 相同
- `source` 相同
- `call_type` 相同
- `system` 未发生根本变化
- `tools` 未发生根本变化

否则新建会话。

### 8.5 不归并条件

以下情况必须新建会话：

- 用户切换了入站 API key，且无法映射到同一业务用户
- 请求消息历史不是前缀延续，而是全新问题
- `system` 提示发生明显变化
- 工具集变化导致语义不再兼容
- 请求之间时间跨度过大

## 9. Postgres 表设计

### 9.1 `session_trajectory_sessions`

字段：

- `id uuid primary key`
- `user_id text not null`
- `source text not null`
- `call_type text not null`
- `provider text not null`
- `canonical_model_family text not null`
- `provider_session_id text null`
- `session_name text null`
- `message_count bigint not null default 0`
- `request_count bigint not null default 0`
- `started_at timestamptz not null`
- `last_activity_at timestamptz not null`
- `closed_at timestamptz null`
- `status text not null`
- `metadata jsonb not null default '{}'::jsonb`

索引：

- `(user_id, last_activity_at desc)`
- `(provider_session_id) where provider_session_id is not null`
- `(source, last_activity_at desc)`

说明：

- `canonical_model_family` 用于归类，例如 `claude`、`gpt`、`gemini`。
- `status` 建议取值：`active`、`closed`、`error`、`archived`。

### 9.2 `session_trajectory_requests`

字段：

- `id uuid primary key`
- `session_id uuid not null references session_trajectory_sessions(id)`
- `user_id text not null`
- `provider_request_id text null`
- `upstream_log_id text null`
- `request_index bigint not null`
- `source text not null`
- `call_type text not null`
- `provider text not null`
- `model text not null`
- `user_agent text null`
- `status text not null`
- `started_at timestamptz not null`
- `ended_at timestamptz null`
- `duration_ms bigint null`
- `input_tokens bigint not null default 0`
- `output_tokens bigint not null default 0`
- `reasoning_tokens bigint not null default 0`
- `cached_tokens bigint not null default 0`
- `total_tokens bigint not null default 0`
- `cost_micro_usd bigint not null default 0`
- `request_json jsonb not null`
- `response_json jsonb null`
- `normalized_json jsonb null`
- `error_json jsonb null`

约束：

- `unique (session_id, request_index)`

索引：

- `(session_id, request_index)`
- `(user_id, started_at desc)`
- `(provider_request_id) where provider_request_id is not null`
- `(upstream_log_id) where upstream_log_id is not null`

### 9.3 `session_trajectory_session_aliases`

字段：

- `provider_session_id text primary key`
- `session_id uuid not null references session_trajectory_sessions(id)`
- `user_id text not null`
- `source text not null`
- `created_at timestamptz not null default now()`
- `updated_at timestamptz not null default now()`

说明：

- 用于把未来观察到的显式 provider session ID 绑定回 canonical session。
- 一个 `provider_session_id` 只能映射一个 canonical session。

### 9.4 `session_trajectory_request_exports`

字段：

- `request_id uuid primary key references session_trajectory_requests(id)`
- `session_id uuid not null references session_trajectory_sessions(id)`
- `export_path text not null`
- `export_index bigint not null`
- `exported_at timestamptz not null`
- `export_version text not null`

说明：

- 记录某次请求已经导出到哪个文件路径。
- 避免重复导出和重复编号。

## 10. 写入与导出流程

### 10.1 在线写入流程

请求进入时：

1. 解析入站 API key，对应到 `user_id`
2. 提取 `provider_session_id`
3. 读取并归一化 `system`、`tools`、`messages`
4. 查找可归并的 canonical session
5. 若未命中则新建 `session_trajectory_sessions`
6. 写入 `session_trajectory_requests`
7. 更新 `session_trajectory_sessions.last_activity_at`
8. 若命中显式 `provider_session_id`，同步更新 `session_trajectory_session_aliases`

### 10.2 导出流程

导出任务按 `session_id` 聚合：

1. 按 `session_id` 读取全部请求
2. 按 `request_index asc` 排序
3. 生成导出目录：
   - `session-data/session-exports/[user_id]_[session_id]/`
4. 为每次请求输出：
   - `[index]_[request_id].json`
5. 将导出路径回写到 `session_trajectory_request_exports`

### 10.3 导出文件内容

导出 JSON 保持第 4 节定义，但补充以下字段：

- `canonical_session_id`
- `provider_session_id`
- `provider_request_id`
- `upstream_log_id`
- `request_index`
- `source`
- `normalized_by`

其中：

- `session_id` 字段保持导出视图主语义，值使用 canonical session ID
- `provider_session_id` 单独保留，避免丢失上游原值

## 11. 归一化字段约定

### 11.1 `user_id`

运行时统一使用业务用户标识，不直接依赖原始 API key。

建议：

- 如果当前业务层已有 `api_key -> user_id` 映射，则直接使用
- 若暂时没有，至少使用脱敏后的 API key 指纹作为过渡 user_id

### 11.2 `source`

建议枚举：

- `claude-cli`
- `claude-vscode`
- `openai-sdk`
- `openai-responses`
- `server-live-capture`
- `manual-import`

### 11.3 `call_type`

建议枚举：

- `anthropic_messages`
- `openai_chat_completions`
- `openai_responses`
- `gemini_generate_content`
- `unknown`

## 12. 兼容与迁移策略

### 12.1 对当前需求文档的兼容

当前文档中“若无法识别则回退为 `unknown-session`”需要调整为：

- 离线导出阶段允许暂时使用 `unknown-session`
- 运行时 PG 主存储阶段必须生成 canonical `session_id`

### 12.2 对现有 billing / usage 架构的兼容

本设计不替代现有：

- `usage_events`
- `api_key_model_daily_usage`
- `api_key_groups`

而是新增一层“会话轨迹存储”。

关系如下：

- `usage_events` 负责计费与额度
- `session_trajectory_requests` 负责会话级原始轨迹
- 二者可通过 `user_id`、`model`、`started_at`、后续新增 `request_id` 关联

### 12.3 对对象存储的兼容

若后续请求体和响应体继续变大，可演进为：

- PG 保留摘要字段与索引字段
- 大体积原始 JSON 落对象存储
- `request_json` / `response_json` 改为对象 key 或裁剪后的 JSON

本阶段先不强制引入对象存储。

## 13. 本轮实现建议

建议分三步实施：

1. 先落 PG 表与 session 归并逻辑
2. 再补导出器，将 PG 请求轨迹导出为 `session-exports`
3. 最后补管理接口与回放分析接口

本轮最小可交付范围：

- `session_trajectory_sessions`
- `session_trajectory_requests`
- `session_trajectory_session_aliases`
- canonical `session_id` 归并逻辑
- 导出目录与单次请求 JSON 生成

本轮暂不做：

- Redis
- SQS / Kafka
- 多消费者异步清洗管道
- MongoDB / DynamoDB 双写

## 14. 风险点

- 不同客户端的消息格式归一化规则若不稳定，可能导致错误归并或过度拆分。
- Claude CLI 一类客户端会重发大体积历史消息，前缀匹配成本需要控制。
- 若同一用户在极短时间并行开多个终端会话，单靠“最近活跃会话 + 前缀匹配”可能出现歧义，后续可增加显式 `client_session_hint`。
- 若后续引入 provider 原生 resume 机制，需要把 canonical session 与 provider session 的多对一关系管理清楚。
