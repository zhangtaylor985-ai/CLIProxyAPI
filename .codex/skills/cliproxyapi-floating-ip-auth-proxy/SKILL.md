---
name: cliproxyapi-floating-ip-auth-proxy
description: Use when adding Hetzner floating IPv4 addresses to the CLIProxyAPI production host and binding a specific Codex auth JSON file to a dedicated outbound IP through a local per-auth HTTP proxy. Trigger for requests like "add floating ip", "bind IP to Codex auth file", "每个 codex auth file 用不同 IP", or "给某个 auth 绑定出口 IP".
---

# CLIProxyAPI Floating IP Auth Proxy

## Scope

Use this on the production host `/root/cliapp/CLIProxyAPI` when a Hetzner floating IPv4 must become the outbound IP for one Codex OAuth auth file under `auths/`.

The established pattern is:

- Add the floating IP to `eth0`.
- Persist it in `/etc/netplan/50-cloud-init.yaml`.
- Run one local HTTP CONNECT proxy per auth file with `/root/cliproxy-localip-proxy/localip-http-proxy`.
- Bind the proxy outbound source with `-source-ip`.
- Set the auth JSON `proxy_url` to `http://127.0.0.1:<port>`.

## Existing Assignments

- `codex-eddyundefined89@gmail.com-pro.json` -> `http://127.0.0.1:18081` -> `95.217.246.176`
- `codex-aritaser346@gmail.com-pro.json` -> `http://127.0.0.1:18082` -> `95.217.242.146`
- `codex-rakibulhassan74857@gmail.com-pro.json` -> `http://127.0.0.1:18083` -> `95.216.180.100`

Use the next unused port, normally `18084+`, for new bindings.

## Procedure

1. Confirm the target auth file by scanning email fields, not only filenames:

```bash
python3 - <<'PY'
import json, pathlib
for p in sorted(pathlib.Path('auths').glob('*.json')):
    try:
        d=json.loads(p.read_text())
    except Exception as e:
        print(f'{p}: invalid json: {e}')
        continue
    if d.get('type') == 'codex':
        print(f'{p}: email={d.get("email")} proxy_url={d.get("proxy_url")} disabled={d.get("disabled")} expired={d.get("expired")}')
PY
```

2. Add the floating IP at runtime:

```bash
sudo ip addr add <floating-ip> dev eth0
ip -4 addr show dev eth0
```

If the address already exists, continue after confirming it is present.

3. Persist the IP in `/etc/netplan/50-cloud-init.yaml` under `eth0.addresses`, then validate:

```bash
netplan generate
```

Do not run `netplan apply` during active production traffic unless needed; the runtime address is already added.

4. Create a systemd unit named `cliproxy-localip-<label>.service`:

```ini
[Unit]
Description=CLIProxyAPI local outbound proxy for <label> Codex auth
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/root/cliproxy-localip-proxy/localip-http-proxy -listen 127.0.0.1:<port> -source-ip <floating-ip>
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

Then start it:

```bash
systemctl daemon-reload
systemctl enable --now cliproxy-localip-<label>.service
systemctl status cliproxy-localip-<label>.service --no-pager -l
```

5. Verify the proxy's actual outbound IP. Unset environment proxies so the test does not hit the shell-level proxy:

```bash
env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY \
  curl -4 --proxy http://127.0.0.1:<port> --max-time 10 -sS https://ifconfig.me
```

The output must exactly match the floating IP.

6. Update the auth JSON:

```json
"proxy_url": "http://127.0.0.1:<port>"
```

If the field is absent, add it at the top level. Keep the JSON valid and preserve the token fields.

7. Confirm CLIProxyAPI hot reload and use:

```bash
python3 -m json.tool auths/<file>.json >/dev/null
journalctl -u cliproxyapi.service --since '2 minutes ago' --no-pager \
  | rg '<auth-file>|auth file changed|via http proxy|proxy setup failed|proxy_unavailable'
```

Success indicators:

- `auth file changed ... processing incrementally`
- subsequent request log for that auth includes `via http proxy`
- no `proxy setup failed` or `proxy_unavailable`

## Notes

- CLIProxyAPI supports `socks5://`, `http://`, and `https://` proxy URLs. The local per-IP proxy uses `http://127.0.0.1:<port>` because it is simple and works for HTTPS upstreams through CONNECT.
- The host may have shell-level `HTTP_PROXY` / `HTTPS_PROXY`; always unset those during outbound-IP verification.
- Do not expose auth file contents, tokens, account emails, upstream model routing, or credential details in user-facing output beyond the specific account the user asked to operate on.
