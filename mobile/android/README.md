# Android shell · 安卓壳

A full-screen WebView that opens your Tianshu console. No business logic, no data of its own, about 17 KB.
一个打开天枢控制台的全屏 WebView，没有自己的业务逻辑和数据，约 17 KB。

- If the console sits behind Cloudflare Access, the sign-in page opens inside the app, so the session stays in the app.
  控制台在 Cloudflare Access 后面时，登录页也在应用里打开，登录状态留在应用里。
- Links to other sites open in the system browser. When the hub is unreachable the app explains why and retries every 8 seconds.
  其他网站的链接交给系统浏览器。连不上时显示原因，每 8 秒重试。
- This is a debug build: WebView debugging is on and it is signed with your local debug key. Do not distribute it.
  这是调试包：开启了 WebView 调试，用本机调试签名，不要分发。

## Build · 构建

Gradle is not needed. The script calls `aapt2`, `javac`, `d8`, `zipalign` and `apksigner` from the Android SDK directly (platform 36, build-tools 36.0.0) and needs JDK 17+ and `~/.android/debug.keystore`.
不用 Gradle，直接调用 Android SDK 的命令行工具。需要 JDK 17+ 和 `~/.android/debug.keystore`。

```bash
ANDROID_SDK=/path/to/android-sdk TIANSHU_URL=https://tianshu.example.com/ bash mobile/android/build.sh
```

Both values can also live in `.local/android-sdk.txt` and `.local/android-url.txt`, which are never committed. Output: `.build/wanglou-debug.apk`.
两个值也可以写进不提交的 `.local/android-sdk.txt` 和 `.local/android-url.txt`。产物在 `.build/wanglou-debug.apk`。

## Install and debug · 安装和调试

```bash
bash mobile/android/install.sh
node mobile/android/cdp.mjs 9223 url
```

`install.sh` installs, starts the app and forwards the WebView DevTools socket to port 9223; `cdp.mjs` can then `eval`, `nav`, `reload` or take a `shot`. You can also use `chrome://inspect` in a desktop Chromium browser.
`install.sh` 安装并启动应用，把 WebView 调试端口转发到 9223；之后可以用 `cdp.mjs` 执行脚本、跳转、刷新或截图，也可以在电脑上的 Chromium 浏览器打开 `chrome://inspect`。

Some ZTE / nubia / RedMagic phones block USB installs (`Caller has no access to session -1`) until you verify your vendor account on the phone or allow USB installs in developer options.
部分中兴、努比亚、红魔手机会拦截 USB 安装，需要先在手机上验证厂商账号，或在开发者选项里允许通过 USB 安装。

## Sign in · 登录

Tap “让电脑批准登录” (let a computer approve) on the sign-in page, then approve the request on a computer that is already signed in after checking that both screens show the same code.
在登录页点“让电脑批准登录”，然后在已登录的电脑上核对两边代码一致后批准。
