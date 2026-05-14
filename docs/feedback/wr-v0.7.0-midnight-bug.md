## Bug: `wr remind push --due` 在 00:00 推送了已完成的历史 meeting

**wr 版本:** v0.7.0  
**发现时间:** 2026-05-14 00:00  
**严重程度:** 中（夜间误推送，打扰用户）

### 复现条件

1. 有一条 meeting 记录，`date` 为昨天，**未设置 `time`**
2. 该 meeting 在昨天已被标记 `completed`
3. 轮询 cron (`*/5 * * * *`) 在午夜 00:00 执行 `wr remind push --due --window 15m`

### 实际现象

于 **2026-05-14 00:00:30** 通过 Pushover 推送了标题为「血液疾病预警项目：疾病清单、收集需求沟通评审」的 meeting，日期标注 2026-05-13，priority 为 high。

该 meeting 在 **2026-05-13 16:44** 就已经标记 completed，不应再次推送。

### 触发原因分析

v0.7.0 新增了 meeting/task 覆盖（#135），但缺少两个过滤条件：

1. **未过滤 completed/cancelled 状态** — 已完成的记录不应参与 `--due` 扫描。v0.7.0 之前 reminder 类型有 auto-complete 逻辑，但 meeting 类型没有相应防护。

2. **未检查 date 是否已过** — `--due` 的时间判断 `date + time ≤ now + window` 在 time 为空时退化为 `date (00:00) ≤ now + window`。当 date = 昨天，now = 今天 00:00，不等式恒成立。

### 涉及记录

```json
{
  "type": "meeting",
  "title": "血液疾病预警项目：疾病清单、收集需求沟通评审",
  "date": "2026-05-13",
  "status": "completed",
  "updated_at": "2026-05-14T00:00:30+08:00",
  "short_id": "6bbea669529cf798",
  "notification_priority": "high"
}
```

### 建议修复

```go
// remind push --due 扫描时增加过滤：
// 1. status != "completed" && status != "cancelled"
// 2. date >= today
// 3. 当 time 为空且 date < today 时，视为已过期，跳过
```

### 影响范围

所有 date 在过去且无 time 字段的 completed meeting 都可能被午夜 cron 重新推送。已完成但没有 time 的历史 task 同理。
