---
name: cliproxyapi-domain-tunnel-proxy
description: Use when checking, documenting, or changing CLIProxyAPI domain routing, Caddy reverse proxies, Cloudflare Tunnel/local hostname proxying, `cc.claudepool.com`, `admin.claudepool.com`, `usage.claudepool.com`, `cc2.claudepool.com`, or worker reverse tunnel deployment boundaries.
---

# CLIProxyAPI Domain Tunnel Proxy

## Scope

这个 skill 记录 CLIProxyAPI 项目的域名、反代与隧道边界。改域名前先确认目标服务是 CLIProxyAPI 还是 Sub2API。

Known hosts:

- Main VPS: `204.168.245.138`
- Worker VPS: `178.105.98.15`
- Backend production repo: `/root/cliapp/CLIProxyAPI`
- Frontend production repo: `/root/cliapp/Cli-Proxy-API-Management-Center`

## Current Routing Model

Always verify live config before changing it:

```bash
ssh root@204.168.245.138 'systemctl cat cliproxyapi.service; systemctl status caddy --no-pager -l; rg -n "cc\\.claudepool\\.com|admin\\.claudepool\\.com|usage\\.claudepool\\.com|management\\.html|127\\.0\\.0\\.1:5173|127\\.0\\.0\\.1:8317|127\\.0\\.0\\.1:8080" /etc/caddy/Caddyfile'
```

Expected project wiring:

- `admin.claudepool.com` -> system Caddy -> `127.0.0.1:5173` -> frontend repo-local Caddy
- `usage.claudepool.com` -> system Caddy -> CLIProxyAPI backend on `127.0.0.1:8317`
- `/management.html` on the production public domain should redirect to `https://admin.claudepool.com/`
- `cc.claudepool.com` may be shared with Sub2API routing; confirm current `/etc/caddy/Caddyfile` before assuming it targets CLIProxyAPI

## Cloudflare Tunnel For Local Domains

For exposing localhost through a fixed or temporary Cloudflare Tunnel hostname, use the user-level `cloudflare-tunnel-local-proxy` skill.

Common fixed hostname:

```text
cc2.claudepool.com
```

Rules:

- Ask for or discover the local service URL before creating a public route.
- Do not print or commit Cloudflare tokens, tunnel credentials, cert files, or full sensitive command lines.
- Record public tunnel starts or DNS/ingress changes with that skill's `record_tunnel_event.py`.
- Verify local service first, then DNS and public routing.

## Worker Reverse Tunnel

The Codex worker path is not a Cloudflare Tunnel. It uses SSH reverse tunnels from the worker VPS to the main VPS.

Known production units on the worker VPS:

```bash
ssh root@178.105.98.15 'systemctl status cliproxy-workers-reverse-tunnel.service --no-pager -l; systemctl status cliproxy-workers-firewall.service --no-pager -l; docker ps --filter name=cliproxy-worker'
```

Worker container local ports are `18317-18324`; the main program accesses them on the main VPS loopback through the reverse tunnel.

## Change Safety

- Production source changes must go through GitHub main; do not hot-edit repository files on the VPS unless the user explicitly approves an emergency hotfix.
- For Caddy changes, back up `/etc/caddy/Caddyfile`, validate it, reload or restart Caddy, and verify public endpoints.
- Keep internal provider/auth/model routing details out of user-facing pages and public API responses.
- Local probes for `127.0.0.1`, `localhost`, or `::1` must bypass global proxies.

## What to Report

Report:

- which domain or tunnel was inspected or changed
- live Caddy or tunnel config source used for truth
- target service and local port
- validation commands and results
- whether a Cloudflare Tunnel event record was written
