# 2026-04-10 管理端角色与 API Key 策略上线审查

## 目标

- 让管理端支持 PG 持久化用户名/密码登录与管理员/员工角色隔离。
- 让员工仅能访问 API Key 策略相关页面，并完成新增、编辑、删除 Key。
- 确认 API Key `name` / `note` 字段、Telegram 通知与权限边界达到可上线标准。

## 当前结论

- 初始审查结论为 `no-go`。
- 当前修复方向：
  - 后端收紧员工权限，移除统计/事件等越权能力。
  - 前端接通用户名密码登录、角色态、路由与导航隔离。
  - 前端补齐 `name` / `note` 字段闭环，并避免员工端触发统计接口。
  - 重新执行前后端构建、测试与行为回归，再给最终发布结论。

## 已完成验证

- 后端回归：`go test ./internal/api/handlers/management ./internal/api ./internal/alerting ./internal/managementauth`
- 管理权限定向回归：`go test ./internal/api/handlers/management -run 'TestManagementLoginAndRoleAuthorization|TestGetAPIKeyRecord_StaffReceivesPolicyOnlyDetailWithoutBillingStore|TestListAPIKeyRecordsLite_FiltersStatusAndGroup|TestPolicyViewRoundTripUsesFamilyAccessTogglesAndMetadata' -count=1`
- 前端类型检查：`npm run type-check`
- 前端生产构建：`npm run build`

## 发布判断

- 当前代码已从先前的 `no-go` 提升到“满足本次功能上线门禁”。
- 仍需注意：
  - 工作树存在大量并行任务改动，本次仅按最小必要范围修复并验证；正式发版前应基于最终合并后的实际树再跑一次相同门禁。
  - 默认种子账号密码仍是源码内 bootstrap 值；生产发布后应尽快轮换。

## 并行任务隔离

- 根目录 `task_plan.md` / `findings.md` / `progress.md` 正被归档任务使用，本任务单独记录在 `tasks/` 下，避免覆盖并行上下文。
