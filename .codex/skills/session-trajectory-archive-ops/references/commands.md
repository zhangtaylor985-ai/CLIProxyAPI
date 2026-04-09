# Session Trajectory Archive 常用命令

## 1. 新开一轮归档

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --inactive-hours 24
```

## 2. 续跑已有游标

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --run-id session-archive-20260408T152942Z
```

## 3. 查看运行状态

```bash
cat /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/run-state.json
```

## 4. 查看归档目录大小

```bash
ls -lah /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>
du -sh /Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>
```

## 5. 查看后台进程

```bash
ps -Ao pid,command | rg 'session_trajectory_archive.py'
```

## 6. 查看 PostgreSQL 当前动作

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
psql "$APIKEY_POLICY_PG_DSN" -At -F $'\t' -c \
  "select state, wait_event_type, wait_event, now()-query_start as age, left(query,160)
   from pg_stat_activity
   where datname='cliproxy' and application_name='psql'
   order by query_start desc limit 5;"
```

## 7. 验证脚本本地测试

```bash
cd /Users/taylor/code/tools/CLIProxyAPI-ori
python3 -m unittest discover -s test -p 'test_session_trajectory_archive.py'
```

## 8. 完成后看最近结果

```bash
cat /Volumes/Storage/CLIProxyAPI-session-archives/latest_completed.json
```
