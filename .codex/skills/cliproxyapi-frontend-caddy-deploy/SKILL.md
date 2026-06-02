---
name: cliproxyapi-frontend-caddy-deploy
description: Use when deploying, verifying, or documenting the production Management Center frontend for CLIProxyAPI on the main VPS, especially `/root/cliapp/Cli-Proxy-API-Management-Center`, repo-local Caddy on `127.0.0.1:5173`, `admin.claudepool.com`, or frontend release handoff after backend/frontend mainline changes.
---

# CLIProxyAPI Frontend Caddy Deploy

## Scope

这个 skill 只处理主程序管理端前端，不处理后端内置旧页面。

- Local source repo: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- Production repo: `/root/cliapp/Cli-Proxy-API-Management-Center`
- Production frontend process: repo-local Caddy serving `dist/index.html` on `127.0.0.1:5173`
- Public entry: system Caddy routes `admin.claudepool.com` to `127.0.0.1:5173`
- Old backend page: `/root/cliapp/CLIProxyAPI/static/management.html` is not the main frontend

## Core Rules

- Release frontend code through GitHub main, not by copying files to production.
- Before pushing or pulling, run `git fetch origin main` and protect unrelated local changes.
- Do not treat backend `/management.html` as the Management Center source or deploy target.
- If backend and frontend changed together, use `cliproxyapi-mainline-release-flow` first, then deploy both sides.
- Localhost probes must bypass global proxies with `NO_PROXY/no_proxy=127.0.0.1,localhost,::1` and, when needed, unset proxy variables for the single command.

## Production Deploy

Run on the main VPS after frontend `origin/main` contains the desired commit:

```bash
cd /root/cliapp/Cli-Proxy-API-Management-Center
git status --short --branch
git fetch origin main
git pull --ff-only origin main
npm ci
bash scripts/redeploy_frontend.sh --no-pull
```

If `scripts/redeploy_frontend.sh` is unavailable or unsuitable, do the manual path:

```bash
cd /root/cliapp/Cli-Proxy-API-Management-Center
npm run build
npm run check:caddy
nohup npm run serve:caddy > .caddy-serve.log 2>&1 &
```

Only use the manual start when `127.0.0.1:5173` is not already served by the repo-local Caddy.

## Verify Routing

Check the repo-local Caddy:

```bash
env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY \
  curl -fsSI http://127.0.0.1:5173/
```

Check the system Caddy route:

```bash
systemctl status caddy --no-pager -l
rg -n 'admin\\.claudepool\\.com|127\\.0\\.0\\.1:5173|management\\.html' /etc/caddy/Caddyfile
curl -fsSI https://admin.claudepool.com/
```

The expected production wiring is:

```text
admin.claudepool.com -> system Caddy -> 127.0.0.1:5173 -> repo-local Caddy -> dist/index.html
```

## What to Report

Report:

- frontend commit deployed
- whether `npm ci`, `npm run build`, and `npm run check:caddy` passed
- whether `127.0.0.1:5173` serves the current `dist/index.html`
- whether `admin.claudepool.com` responds through system Caddy
- whether backend deployment was also required or already handled
