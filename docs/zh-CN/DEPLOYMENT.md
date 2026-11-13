# 部署

天枢服务是一个静态编译的程序，只监听本机回环地址，放在负责 HTTPS 的反向代理或隧道后面。下面用 systemd、Cloudflare Tunnel 和 Cloudflare Access 举例，换成其他 HTTPS 反向代理也是同样的思路。

## 构建

```bash
bash scripts/build.sh
```

它会构建控制台、运行 `go vet` 和测试，把当前平台的程序输出到 `.build/`，Linux amd64 版输出到 `.build/linux/`。控制台嵌在程序里，自己执行 `go build` 时要先构建网页部分。

## 安装服务（Linux + systemd）

```bash
useradd --system --no-create-home --shell /usr/sbin/nologin wanglou
install -d -o wanglou -g wanglou -m 0700 /var/lib/wanglou /var/backups/wanglou
install -d -m 0755 /srv/apps/wanglou/releases/0.1.0
install -m 0755 .build/linux/wanglou /srv/apps/wanglou/releases/0.1.0/wanglou
ln -sfn releases/0.1.0 /srv/apps/wanglou/current
install -d -m 0750 -o root -g wanglou /etc/wanglou
install -m 0640 -o root -g wanglou deploy/env.example /etc/wanglou/env   # 然后按需修改
install -m 0644 deploy/wanglou.service deploy/wanglou-backup.service deploy/wanglou-backup.timer /etc/systemd/system/

sudo -u wanglou sh -c 'set -a; . /etc/wanglou/env; /srv/apps/wanglou/current/wanglou init'   # 管理员令牌只显示这一次
systemctl daemon-reload
systemctl enable --now wanglou wanglou-backup.timer
curl -s http://127.0.0.1:8787/healthz
```

服务运行在严格的 systemd 沙箱里，内存上限 256 MB。备份定时器每天做一次一致性的 SQLite 快照，保留 14 天。

### 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `WANGLOU_ADDR` | `127.0.0.1:8787` | 监听地址 |
| `WANGLOU_DATA` | `./data` | 数据目录 |
| `WANGLOU_VANTAGE` | `hub` | 探测发出位置的名称 |
| `WANGLOU_PUBLIC_URL` | 空 | 公网 HTTPS 地址，设置后启用 OAuth 和 `Secure` Cookie |
| `WANGLOU_TRUST_CF` | 空 | 设为 `1` 时客户端地址取 `CF-Connecting-IP` |
| `WANGLOU_GITHUB` | 开 | 设为 `off` 关闭 GitHub 同步 |
| `WANGLOU_GITHUB_TOKEN` | 空 | 只读令牌，没有时尝试 `gh auth token` |
| `WANGLOU_GITHUB_WORKSPACE` | `personal` | 新发现的仓库放进哪个空间 |
| `WANGLOU_OAUTH_CIMD` | 开 | 服务器访问不到 `chatgpt.com` 等客户端主机时（比如在国内）设为 `off`：元数据不再声明支持 Client ID Metadata Document，ChatGPT 等客户端改用动态注册 |

## 安全地对外开放

1. 给你的域名加 DNS 记录和隧道规则，指向 `http://127.0.0.1:8787`。
2. 控制台放在身份认证代理后面。用 Cloudflare Access 时：一个覆盖整个域名的应用，只允许你的邮箱；另一个应用用**绕过**策略，放行自带认证的路径：`/mcp`、`/oauth`、`/.well-known`、`/api/v1/ingest`、`/healthz`。
3. 先建好 Access，再让隧道开放控制台。在此之前，隧道规则只放行上面这几个路径。
4. 设置 `WANGLOU_PUBLIC_URL` 和 `WANGLOU_TRUST_CF=1` 后重启，确认匿名访问 `/` 会跳到登录页，而 `/healthz` 正常返回。

隧道同时承载其他站点时，换配置可以不中断：先用新配置起一个临时的第二连接器，等它注册成功，再重启正式连接器，最后停掉临时的。

## 采集器

在“访问”页新建采集器身份（权限 `ingest`，限定可上报的设备），把令牌存成文件。在每台 Linux 主机上：

```bash
bash deploy/install-agent.sh <设备 slug> https://tianshu.example.com ./token
```

要采集日志，把它加入日志读取组：`usermod -aG systemd-journal wanglou-agent && systemctl restart wanglou-agent`。采集器以无特权用户运行，内存上限 64 MB，CPU 上限 10%。卸载方法写在脚本开头。

Windows 上把 `deploy/agent.example.yaml` 复制成 `.local/agent.yaml`，运行 `start-online.cmd`；服务器地址和 SSH 别名写在 `.local/online-env.cmd`（格式见脚本里的注释）。采集器配置里的 `ssh:` 段用来采集不能装软件的主机。

手机可以用 Termux（`deploy/phone/termux-report.sh`）、Tasker 或 iOS 快捷指令，向 `/api/v1/ingest/heartbeat` 发心跳。

## 接入 Agent

```bash
claude mcp add --transport http tianshu https://tianshu.example.com/mcp \
  --header "Authorization: Bearer $WANGLOU_TOKEN"
```

Codex（`~/.codex/config.toml`）：

```toml
[mcp_servers.tianshu]
url = "https://tianshu.example.com/mcp"
bearer_token_env_var = "WANGLOU_TOKEN"
```

支持 OAuth 远程 MCP 的云端客户端只需要填 `https://tianshu.example.com/mcp`，在控制台里批准并选择可见空间。

## 升级和回退

把新程序放到 `releases/<版本>/`，把 `current` 指过去再重启。回退就把 `current` 指回上一版。表结构只做增量迁移。

## 备份和恢复

`wanglou backup <文件>` 在线做快照，目标已存在时拒绝。恢复：停服务，把快照复制成数据目录里的 `wanglou.db`，再启动。指标和信号会由采集器重新填上。恢复后检查身份列表：旧备份可能带回已经吊销的身份。
