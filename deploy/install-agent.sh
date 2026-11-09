#!/usr/bin/env bash
# 在一台 Linux 主机上安装天枢采集器（程序名 wanglou-agent）。需要 root，需要已授权。
#
#   用法：bash install-agent.sh <设备slug> <hub地址> <令牌文件>
#
# 这个脚本会创建专用用户、安装二进制和 systemd 单元，并启动服务。
# 卸载：systemctl disable --now wanglou-agent
#       rm -rf /etc/wanglou-agent /usr/local/bin/wanglou-agent /etc/systemd/system/wanglou-agent.service
#       userdel wanglou-agent
set -euo pipefail

device=${1:?缺少设备 slug}
hub=${2:?缺少 hub 地址}
token_file=${3:?缺少令牌文件}
here=$(cd "$(dirname "$0")" && pwd)

[ "$(id -u)" = 0 ] || { echo "需要 root" >&2; exit 1; }
[ -f "$here/wanglou-agent" ] || { echo "找不到 wanglou-agent 二进制" >&2; exit 1; }

id wanglou-agent >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin wanglou-agent
install -m 0755 "$here/wanglou-agent" /usr/local/bin/wanglou-agent
install -d -m 0750 -o root -g wanglou-agent /etc/wanglou-agent
install -m 0640 -o root -g wanglou-agent "$token_file" /etc/wanglou-agent/token
cat > /etc/wanglou-agent/agent.yaml <<EOF
hub: $hub
token_file: /etc/wanglou-agent/token
device: $device
interval_s: 30
EOF
chown root:wanglou-agent /etc/wanglou-agent/agent.yaml
chmod 0640 /etc/wanglou-agent/agent.yaml
install -m 0644 "$here/wanglou-agent.service" /etc/systemd/system/wanglou-agent.service
systemctl daemon-reload
systemctl enable --now wanglou-agent
sleep 2
systemctl is-active wanglou-agent
journalctl -u wanglou-agent -n 5 --no-pager
