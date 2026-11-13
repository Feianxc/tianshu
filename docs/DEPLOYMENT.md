# Deployment

The hub is a single static binary. It listens on loopback and is meant to sit behind a reverse proxy or a tunnel that terminates HTTPS. The steps below use systemd and Cloudflare Tunnel with Cloudflare Access; any HTTPS reverse proxy works the same way.

## Build

```bash
bash scripts/build.sh
```

This builds the console, runs `go vet` and the tests, and writes binaries for the current platform to `.build/` and Linux amd64 binaries to `.build/linux/`. The console is embedded into the binary, so build the web part first if you call `go build` yourself.

## Install the hub (Linux, systemd)

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin wanglou
install -d -o wanglou -g wanglou -m 0700 /var/lib/wanglou /var/backups/wanglou
install -d -m 0755 /srv/apps/wanglou/releases/0.1.0
install -m 0755 .build/linux/wanglou /srv/apps/wanglou/releases/0.1.0/wanglou
ln -sfn releases/0.1.0 /srv/apps/wanglou/current
install -d -m 0750 -o root -g wanglou /etc/wanglou
install -m 0640 -o root -g wanglou deploy/env.example /etc/wanglou/env   # then edit it
install -m 0644 deploy/wanglou.service deploy/wanglou-backup.service deploy/wanglou-backup.timer /etc/systemd/system/

sudo -u wanglou sh -c 'set -a; . /etc/wanglou/env; /srv/apps/wanglou/current/wanglou init'   # prints the admin token once
systemctl daemon-reload
systemctl enable --now wanglou wanglou-backup.timer
curl -s http://127.0.0.1:8787/healthz
```

The unit runs under a strict systemd sandbox with a 256 MB memory limit. The backup timer writes a consistent SQLite snapshot every day and keeps 14 days.

### Environment

| Variable | Default | Meaning |
|---|---|---|
| `WANGLOU_ADDR` | `127.0.0.1:8787` | Listen address |
| `WANGLOU_DATA` | `./data` | Data directory |
| `WANGLOU_VANTAGE` | `hub` | Name of the place probes run from |
| `WANGLOU_PUBLIC_URL` | empty | Public HTTPS URL. Enables OAuth and `Secure` cookies |
| `WANGLOU_TRUST_CF` | empty | `1` to take the client address from `CF-Connecting-IP` |
| `WANGLOU_GITHUB` | on | `off` disables the GitHub connector |
| `WANGLOU_GITHUB_TOKEN` | empty | Read-only token; falls back to `gh auth token` |
| `WANGLOU_GITHUB_WORKSPACE` | `personal` | Workspace for newly discovered repositories |
| `WANGLOU_OAUTH_CIMD` | on | `off` when the hub cannot reach `chatgpt.com` and the other client hosts (for example behind a national firewall). The metadata then stops advertising Client ID Metadata Documents and clients such as ChatGPT use dynamic registration instead |

## Expose it safely

1. Add a DNS record and a tunnel ingress rule for your hostname pointing at `http://127.0.0.1:8787`.
2. Put the console behind an identity-aware proxy. With Cloudflare Access: one self-hosted application for the whole hostname that allows only your email, and a second application with a **bypass** policy for the paths that carry their own authentication: `/mcp`, `/oauth`, `/.well-known`, `/api/v1/ingest`, `/healthz`.
3. Create the Access applications **before** the tunnel serves the console. Until then, restrict the ingress rule to the paths above.
4. Set `WANGLOU_PUBLIC_URL` and `WANGLOU_TRUST_CF=1`, restart, and check that an anonymous request to `/` is redirected to the login page while `/healthz` answers.

When a shared tunnel serves other sites, change its configuration without downtime: start a second, temporary connector with the new configuration, wait until it registers, restart the main connector, then stop the temporary one.

## Collectors

Create a collector identity on the Access page (scope `ingest`, limited to its device slugs) and save the token to a file. On each Linux host:

```bash
bash deploy/install-agent.sh <device-slug> https://tianshu.example.com ./token
```

To collect logs, add the user to the journal readers: `usermod -aG systemd-journal wanglou-agent && systemctl restart wanglou-agent`. The agent runs as an unprivileged user with a 64 MB memory and 10 % CPU limit. Uninstall instructions are at the top of the script.

On Windows, copy `deploy/agent.example.yaml` to `.local/agent.yaml` and run `start-online.cmd`; put your server URL and SSH alias in `.local/online-env.cmd` (see the comments in the script). The `ssh:` section of the agent configuration polls hosts where nothing can be installed.

Phones can report through Termux (`deploy/phone/termux-report.sh`), Tasker or iOS Shortcuts by posting a heartbeat to `/api/v1/ingest/heartbeat`.

## Connect agents

```bash
claude mcp add --transport http tianshu https://tianshu.example.com/mcp \
  --header "Authorization: Bearer $WANGLOU_TOKEN"
```

Codex (`~/.codex/config.toml`):

```toml
[mcp_servers.tianshu]
url = "https://tianshu.example.com/mcp"
bearer_token_env_var = "WANGLOU_TOKEN"
```

Cloud clients that support remote MCP servers with OAuth only need the URL `https://tianshu.example.com/mcp`; you approve the connection and pick the visible workspaces in the console.

## Upgrade and roll back

Upload the new binary to `releases/<version>/`, point `current` at it and restart. Roll back by pointing `current` at the previous release. The schema only uses additive migrations.

## Backup and restore

`wanglou backup <file>` takes an online snapshot and refuses to overwrite. To restore, stop the service, copy the snapshot to `wanglou.db` in the data directory and start it again. Collectors refill metrics and signals. Check the identity list afterwards: an old backup may bring back revoked identities.
