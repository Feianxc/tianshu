import { useEffect, useRef, useState } from "react";
import { ApiError, api, useTick } from "./lib";

// 设备配对的两端：新设备在登录页发起请求、显示代码、等待；
// 已登录的电脑弹出请求卡片，核对代码后批准。新设备从头到尾不输入令牌。

interface Started {
  id: string;
  code: string;
  secret: string;
  expires_at: string;
}

type Phase = { k: "idle" } | { k: "wait"; p: Started } | { k: "done"; msg: string };

function left(iso: string) {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

export function PairLogin({ onDone }: { onDone: () => void }) {
  const [ph, setPh] = useState<Phase>({ k: "idle" });
  const [busy, setBusy] = useState(false);
  const [, tick] = useState(0);
  const start = async () => {
    setBusy(true);
    try {
      setPh({ k: "wait", p: await api.post<Started>("/api/v1/pairing", {}) });
    } catch (x) {
      setPh({ k: "done", msg: (x as Error).message });
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    if (ph.k !== "wait") return;
    let live = true;
    const t = setInterval(async () => {
      tick((n) => n + 1);
      try {
        const r = await api.post<{ state: string }>(`/api/v1/pairing/${ph.p.id}/poll`, { secret: ph.p.secret });
        if (!live) return;
        if (r.state === "approved") onDone();
        else if (r.state === "denied") setPh({ k: "done", msg: "电脑上拒绝了这次请求。" });
      } catch (x) {
        if (!live) return;
        if (x instanceof ApiError && x.status === 404) setPh({ k: "done", msg: "请求已过期，重新发起一次。" });
        // 其他错误多半是网络抖动，下一轮再试
      }
    }, 2000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [ph, onDone]);

  if (ph.k === "wait") {
    return (
      <div className="pair-wait" role="status">
        <p>在已经登录天枢的电脑上批准这次登录，并核对两边的代码一致。</p>
        <b className="pair-code">{ph.p.code}</b>
        <p className="pair-meta"><span className="mono">{left(ph.p.expires_at)}</span> 后失效 · 每 2 秒检查一次</p>
        <button type="button" className="ghost" onClick={() => setPh({ k: "idle" })}>取消</button>
      </div>
    );
  }
  return (
    <div className="pair-start">
      <button type="button" onClick={start} disabled={busy}>{busy ? "正在发起" : "让电脑批准登录"}</button>
      <p>{ph.k === "done" ? ph.msg : "不用输入令牌：在已登录的电脑上点一下批准就行。"}</p>
    </div>
  );
}

interface Pending {
  id: string;
  code: string;
  user_agent: string;
  ip: string;
  created_at: string;
  expires_at: string;
}

export function deviceLabel(ua: string) {
  const android = ua.match(/Android [\d.]+;\s*([^;)]+?)(?:\s+Build\/[^;)]*)?(?:;|\))/);
  const model = android?.[1]?.trim();
  const named = model && model !== "K" && model !== "wv" ? ` · ${model}` : "";
  if (/WanglouShell/.test(ua)) return `天枢安卓应用${named}`;
  if (android) return `安卓浏览器${named}`;
  if (/iPhone|iPad/.test(ua)) return "iPhone 或 iPad";
  if (/Windows/.test(ua)) return "Windows 电脑";
  if (/Mac OS X/.test(ua)) return "Mac";
  return "未知设备";
}

// 已登录的管理员才会看到：有设备请求登录时在页面顶部弹出。
export function PairAlerts() {
  const [items, setItems] = useState<Pending[]>([]);
  const [hours, setHours] = useState(12);
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);
  useTick();
  const load = async () => {
    const id = ++seq.current;
    try {
      const r = await api.get<{ items: Pending[] }>("/api/v1/pairings");
      if (id === seq.current) setItems(r.items.sort((a, b) => a.created_at.localeCompare(b.created_at)));
    } catch {
      /* 没权限或网络问题时不弹 */
    }
  };
  useEffect(() => {
    load();
    const t = setInterval(() => document.visibilityState === "visible" && load(), 3000);
    return () => clearInterval(t);
  }, []);
  if (items.length === 0) return null;
  const p = items[0];
  const decide = async (approve: boolean) => {
    setBusy(true);
    try {
      await api.post(`/api/v1/pairings/${p.id}`, { approve, hours });
    } catch {
      /* 已过期或已被处理，刷新列表即可 */
    } finally {
      setBusy(false);
      load();
    }
  };
  return (
    <aside className="pair-alert" role="alertdialog" aria-label="设备登录请求">
      <header>
        <strong>有设备请求登录</strong>
        {items.length > 1 && <span className="mono">另有 {items.length - 1} 个</span>}
      </header>
      <p className="pair-device">{deviceLabel(p.user_agent)}<span className="mono">{p.ip}</span></p>
      <b className="pair-code">{p.code}</b>
      <p className="pair-note">先核对这台设备上显示的代码。对不上，或者不是你本人正在操作，就拒绝。</p>
      <div className="pair-actions">
        <select aria-label="登录有效期" value={hours} onChange={(e) => setHours(Number(e.target.value))}>
          <option value={12}>12 小时</option>
          <option value={168}>7 天</option>
        </select>
        <button className="primary" disabled={busy} onClick={() => decide(true)}>批准</button>
        <button disabled={busy} onClick={() => decide(false)}>拒绝</button>
      </div>
      <p className="pair-meta"><span className="mono">{left(p.expires_at)}</span> 后自动失效</p>
    </aside>
  );
}
