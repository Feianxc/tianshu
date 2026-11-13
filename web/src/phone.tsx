import { useEffect, useState } from "react";
import { useApp } from "./app";
import { api, useData } from "./lib";
import type { Card } from "./types";
import { Ago, Panel } from "./ui";

// 只在天枢安卓应用里出现：给这部手机签一个只能上报它自己的采集器令牌，交给应用保存，
// 应用每 15 分钟上报一次电量、网络、内存和存储。令牌不经过页面以外的任何地方，页面也读不回来。

interface Shell {
  info(): string;
  enableReporting(token: string, device: string): string;
  sendNow(): string;
  disableReporting(): void;
}

interface ShellInfo {
  model?: string;
  android?: string;
  reporting?: boolean;
  device?: string;
  last_ok?: number;
  last_code?: number;
  interval_s?: number;
}

export function shell(): Shell | null {
  return (window as unknown as { TianshuShell?: Shell }).TianshuShell ?? null;
}

export function PhoneReporting() {
  const { can } = useApp();
  const sh = shell();
  const [info, setInfo] = useState<ShellInfo>({});
  const [msg, setMsg] = useState("");
  const [busy, setBusy] = useState(false);
  const { data } = useData<{ items: Card[] }>(sh ? "/api/v1/entities?kind=device&limit=200" : null);
  const phones = (data?.items ?? []).filter((c) => c.device?.type === "phone");
  const [device, setDevice] = useState("");
  const refresh = () => {
    try {
      setInfo(JSON.parse(sh?.info() ?? "{}"));
    } catch {
      setInfo({});
    }
  };
  useEffect(refresh, []);
  useEffect(() => {
    if (!device && phones.length > 0) setDevice(info.device || phones[0].slug);
  }, [phones, info.device, device]);
  if (!sh || !can("admin")) return null;

  const enable = async () => {
    setBusy(true);
    setMsg("");
    try {
      const day = new Date().toISOString().slice(0, 10);
      const r = await api.post<{ token: string }>("/api/v1/principals", {
        kind: "collector", name: `phone-app:${device}:${day}:${Math.random().toString(36).slice(2, 6)}`,
        scopes: ["ingest"], devices: [device], workspaces: [], ttl_days: 365,
      });
      const err = sh.enableReporting(r.token, device);
      setMsg(err ? `已保存，但第一次上报失败：${err}` : "已开启，第一次上报成功。");
    } catch (x) {
      setMsg((x as Error).message);
    } finally {
      setBusy(false);
      refresh();
    }
  };
  const now = () => {
    const err = sh.sendNow();
    setMsg(err ? `上报失败：${err}` : "已上报。");
    refresh();
  };
  const stop = () => {
    sh.disableReporting();
    setMsg("已停止。令牌仍在“访问”页的身份列表里，需要时在那里吊销。");
    refresh();
  };

  return (
    <Panel title="这部手机" className="phone-panel" note={info.model ? `${info.model} · Android ${info.android}` : undefined}>
      {info.reporting ? (
        <div className="phone-state">
          <p><span className="phone-dot on" />正在上报为 <b className="mono">{info.device}</b>，每 {Math.round((info.interval_s ?? 900) / 60)} 分钟一次。
            {info.last_ok ? <> 上次成功 <Ago t={new Date(info.last_ok).toISOString()} />。</> : " 还没有成功过。"}</p>
          <div className="phone-actions">
            <button onClick={now}>立即上报</button>
            <button className="ghost" onClick={stop}>停止上报</button>
          </div>
        </div>
      ) : (
        <div className="phone-state">
          <p><span className="phone-dot" />让这部手机定时上报电量、充电、网络、内存和存储。系统最短 15 分钟一次，省电模式下可能更久。</p>
          {phones.length === 0 ? <p className="log-hint">目录里还没有手机类型的设备，先在“设备”页登记一台。</p> : (
            <div className="phone-actions">
              <select aria-label="上报为哪台设备" value={device} onChange={(e) => setDevice(e.target.value)}>
                {phones.map((p) => <option key={p.id} value={p.slug}>{p.name}</option>)}
              </select>
              <button className="primary" disabled={busy || !device} onClick={enable}>{busy ? "正在开启" : "开启上报"}</button>
            </div>
          )}
        </div>
      )}
      {msg && <p className="phone-msg" role="status">{msg}</p>}
    </Panel>
  );
}
