#!/usr/bin/env bash
# 把天枢装到 USB 连着的安卓手机上并打开，同时转发 WebView 调试端口到电脑的 9223。
# 前提：电脑上天枢在运行（dev-start.cmd），.build/wanglou-debug.apk 已经由 build.sh 生成。
# 努比亚/中兴系统第一次通过 USB 安装时，手机上会要求输入努比亚账号密码，要本人在手机上完成。
set -euo pipefail
export MSYS_NO_PATHCONV=1
cd "$(dirname "$0")/../.."
ADB="${ADB:-adb}"
command -v "$ADB" >/dev/null || ADB="$LOCALAPPDATA/Microsoft/WinGet/Packages/Google.PlatformTools_Microsoft.Winget.Source_8wekyb3d8bbwe/platform-tools/adb.exe"
PKG=online.feian.wanglou

"$ADB" reverse tcp:8787 tcp:8787
"$ADB" install -r .build/wanglou-debug.apk
"$ADB" shell am start -n "$PKG/.MainActivity" >/dev/null
for _ in 1 2 3 4 5 6 7 8 9 10; do PID=$("$ADB" shell pidof "$PKG" | tr -d '\r'); [ -n "$PID" ] && break; sleep 1; done
"$ADB" forward tcp:9223 "localabstract:webview_devtools_remote_$PID"
echo "已启动，进程 $PID。调试：node mobile/android/cdp.mjs 9223 url"
