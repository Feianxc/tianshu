#!/data/data/com.termux/files/usr/bin/bash
# Android 手机上报电量和网络。需要 Termux、Termux:API 两个 App 和 pkg install termux-api jq curl。
#
# 定时运行（系统最短 15 分钟，国产 ROM 可能更久，要实测）：
#   termux-job-scheduler --script ~/wanglou/termux-report.sh --period-ms 900000 --persisted true
#
# 手机不要把令牌写在这里，放在 ~/.wanglou-token，权限 600。

hub="${WANGLOU_HUB:-https://tianshu.example.com}"
device="${WANGLOU_DEVICE:-phone-android}"
token=$(cat ~/.wanglou-token) || exit 1

battery=$(termux-battery-status) || exit 1
pct=$(echo "$battery" | jq '.percentage')
plugged=$(echo "$battery" | jq '.plugged != "UNPLUGGED"')
network=$(termux-wifi-connectioninfo 2>/dev/null | jq -r '.supplicant_state // empty')
[ "$network" = "COMPLETED" ] && network="wifi" || network="mobile"

curl -sS -m 20 -X POST "$hub/api/v1/ingest/heartbeat" \
  -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
  -d "{\"device\":\"$device\",\"collector\":\"termux\",\"interval_s\":900,\"battery_pct\":$pct,\"charging\":$plugged,\"network\":\"$network\"}"
