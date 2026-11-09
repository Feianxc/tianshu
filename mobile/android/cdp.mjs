// 真机调试：通过 DevTools 协议控制手机上天枢壳里的 WebView。
// 先转发调试端口：adb forward tcp:9223 localabstract:webview_devtools_remote_<进程号>
// 用法：node cdp.mjs <端口> url | nav <地址> | eval <表达式> | shot <文件.png> | reload
import { writeFileSync } from "node:fs";

const [port, cmd, arg] = process.argv.slice(2);
const pages = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
const page = pages.find((p) => p.type === "page") ?? pages[0];
if (!page) throw new Error("没有可调试的页面");
if (cmd === "url") {
  console.log(page.url);
  process.exit(0);
}
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((ok, bad) => { ws.onopen = ok; ws.onerror = bad; });
let seq = 0;
const waiting = new Map();
ws.onmessage = (m) => {
  const d = JSON.parse(m.data);
  if (d.id && waiting.has(d.id)) { waiting.get(d.id)(d); waiting.delete(d.id); }
};
const send = (method, params = {}) => new Promise((ok) => { const id = ++seq; waiting.set(id, ok); ws.send(JSON.stringify({ id, method, params })); });

if (cmd === "nav") {
  await send("Page.navigate", { url: arg });
  await new Promise((r) => setTimeout(r, 2500));
  console.log((await send("Runtime.evaluate", { expression: "location.href", returnByValue: true })).result.result.value);
} else if (cmd === "reload") {
  await send("Page.reload", { ignoreCache: true });
} else if (cmd === "eval") {
  const r = await send("Runtime.evaluate", { expression: arg, awaitPromise: true, returnByValue: true });
  console.log(JSON.stringify(r.result.result.value ?? r.result.exceptionDetails?.exception?.description ?? r.result, null, 1));
} else if (cmd === "shot") {
  const r = await send("Page.captureScreenshot", { format: "png" });
  writeFileSync(arg, Buffer.from(r.result.data, "base64"));
  console.log(arg);
}
ws.close();
