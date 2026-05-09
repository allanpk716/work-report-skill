# wr digest 调度器：标准 cron 表达式被拒绝

## 版本
wr daemon v0.1.0

## 严重程度
高 — 所有 digest 定时摘要功能完全不可用

## 复现步骤

```bash
wr digest add --schedule "10 8 * * *" --scope today --direction agenda
wr digest add --schedule "0 21 * * *" --scope today --direction summary
wr digest add --schedule "20 8 * * 1" --scope week --direction agenda
```

重启 daemon 后查看日志。

## 实际行为

日志反复出现 (`~/.work-report/logs/daemon--*.log`)：

```
level=warning msg="[digest-scheduler] sync: invalid cron expression, skipping"
  digest_id=d_20260509_7a26a0 schedule="10 8 * * *"
  digest_id=d_20260509_3517ed schedule="0 21 * * *"
  digest_id=d_20260509_8bcc48 schedule="20 8 * * 1"
```

```
level=info msg="[digest-scheduler] sync complete" entry_count=0
```

三个表达式均被拒绝，调度器注册 0 个条目，定时摘要永不触发。

## 预期行为

`10 8 * * *`、`0 21 * * *`、`20 8 * * 1` 均为标准 5 字段 cron，应被 Go cron 库（robfig/cron）正常解析并调度。

## 附加影响

daemon 启动后调度器立即退出，但 HTTP 端口仍监听。`wr agent daemon ensure-running` 检测到进程存活就返回 `already_running`，掩盖了调度器已死的事实。

## 日志片段

```
2026-05-09 16:41:09 daemon started pid=3003
2026-05-09 16:41:09 [digest-scheduler] started
2026-05-09 16:41:09 [digest-scheduler] sync: invalid cron expression, skipping x3
2026-05-09 16:41:09 [digest-scheduler] sync complete entry_count=0
2026-05-09 16:41:09 daemon listening pid=3003
2026-05-09 16:41:09 [digest-scheduler] stopped
2026-05-09 16:41:09 daemon exiting
```

## 环境

- OS: Ubuntu 24.04 (WSL2)
- wr binary: v0.1.0
- Go daemon 内嵌版本: v0.1.0
- 时区: Asia/Shanghai