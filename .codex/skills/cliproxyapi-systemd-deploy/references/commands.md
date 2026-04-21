# CLIProxyAPI Systemd Deploy Commands

## Inspect live unit

```bash
systemctl cat cliproxyapi.service
```

## Check git status before pull or push

```bash
git status --short --branch
git fetch origin main
git log --oneline --decorate --max-count=5 HEAD..origin/main
git log --oneline --decorate --max-count=5 origin/main..HEAD
```

## Rebuild binary

```bash
VERSION=$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" \
  -o ./bin/cliproxyapi ./cmd/server
stat -c '%y %s %n' ./bin/cliproxyapi
```

## Restart and verify

```bash
sudo systemctl restart cliproxyapi.service && sleep 2
systemctl status cliproxyapi.service --no-pager -l
journalctl -u cliproxyapi.service -n 80 --no-pager -l
curl -fsS http://127.0.0.1:8317/
```

## Safe stash before rebase or pull

```bash
git stash push -u -m "pre-deploy-$(date -u +%Y%m%dT%H%M%SZ)"
git stash list --max-count=3
```
