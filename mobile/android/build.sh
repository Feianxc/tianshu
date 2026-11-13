#!/usr/bin/env bash
# 不用 Gradle，直接用 Android SDK 的命令行工具打调试包。产物：.build/wanglou-debug.apk
# 需要：JDK 17+、Android SDK（platforms/android-36、build-tools/36.0.0）、~/.android/debug.keystore
# SDK 位置用 ANDROID_SDK 或 ANDROID_HOME 指定，也可以写进 .local/android-sdk.txt。
# 应用打开的地址用 TIANSHU_URL 指定，也可以写进 .local/android-url.txt。
set -euo pipefail
cd "$(dirname "$0")"
LOCAL=../../.local
SDK="${ANDROID_SDK:-${ANDROID_HOME:-$(cat "$LOCAL/android-sdk.txt" 2>/dev/null || true)}}"
[ -n "$SDK" ] || { echo "设置 ANDROID_SDK 或 ANDROID_HOME" >&2; exit 1; }
URL="${TIANSHU_URL:-$(cat "$LOCAL/android-url.txt" 2>/dev/null || echo https://tianshu.example.com/)}"
BT="$SDK/build-tools/36.0.0"
JAR="$SDK/platforms/android-36/android.jar"
KS="${DEBUG_KEYSTORE:-$HOME/.android/debug.keystore}"
OUT=../../.build
W=$OUT/android
rm -rf "$W" && mkdir -p "$W/res" "$W/gen" "$W/classes" "$W/dex"

"$BT/aapt2.exe" compile --dir res -o "$W/res.zip"
# 地址做成资源，在构建时生成，不进源码
mkdir -p "$W/gen-res/values"
printf '<?xml version="1.0" encoding="utf-8"?>
<resources><string name="hub_url" translatable="false">%s</string></resources>
' "$URL" > "$W/gen-res/values/hub.xml"
"$BT/aapt2.exe" compile --dir "$W/gen-res" -o "$W/hub.zip"
"$BT/aapt2.exe" link -I "$JAR" --manifest AndroidManifest.xml -o "$W/base.apk" --java "$W/gen" \
  --min-sdk-version 26 --target-sdk-version 36 --version-code 3 --version-name 0.3.0 --debug-mode "$W/res.zip" "$W/hub.zip"

javac -source 8 -target 8 -bootclasspath "$JAR" -Xlint:-options -encoding UTF-8 -d "$W/classes" \
  $(find src "$W/gen" -name '*.java')
cmd //c "$(cygpath -w "$BT/d8.bat")" --min-api 26 --lib "$(cygpath -w "$JAR")" --output "$(cygpath -w "$W/dex")" \
  $(find "$W/classes" -name '*.class' -exec cygpath -w {} \;)

# 把 classes.dex 放进包里，再对齐、签名。
python -I - "$W/base.apk" "$W/dex/classes.dex" <<'PY'
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "a", zipfile.ZIP_DEFLATED) as z:
    z.write(sys.argv[2], "classes.dex")
PY
"$BT/zipalign.exe" -f -p 4 "$W/base.apk" "$W/aligned.apk"
cmd //c "$(cygpath -w "$BT/apksigner.bat")" sign --ks "$(cygpath -w "$KS")" --ks-pass pass:android --key-pass pass:android \
  --out "$(cygpath -w "$OUT/wanglou-debug.apk")" "$(cygpath -w "$W/aligned.apk")"
cmd //c "$(cygpath -w "$BT/apksigner.bat")" verify "$(cygpath -w "$OUT/wanglou-debug.apk")"
ls -la "$OUT/wanglou-debug.apk"
