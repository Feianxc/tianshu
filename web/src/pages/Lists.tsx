import { useMemo, useState } from "react";
import { useApp } from "../app";
import { deviceTypeLabel, useData } from "../lib";
import type { Card } from "../types";
import { Ago, Empty, Failure, HealthChip, Lamp, Panel, Unconfirmed } from "../ui";
import { DeviceCard } from "./Overview";
import { EntityEditor } from "./Editor";

function useCards(kind: string, extra = "") {
  const { space } = useApp();
  const qs = `kind=${kind}&limit=500${space ? `&workspace=${space}` : ""}${extra}`;
  return useData<{ items: Card[]; has_more: boolean }>(`/api/v1/entities?${qs}`, 30000);
}

const healthRank: Record<string, number> = { down: 0, degraded: 1, unknown: 2, ok: 3, offline: 4, paused: 5, planned: 6, retired: 7 };

function Title({ h, sub, action }: { h: string; sub?: string; action?: React.ReactNode }) {
  return (
    <header className="page-head">
      <div>
        <h1>{h}</h1>
        {sub && <p>{sub}</p>}
      </div>
      {action}
    </header>
  );
}

export function DevicesPage() {
  const { can } = useApp();
  const { data, error, reload } = useCards("device");
  const [adding, setAdding] = useState(false);
  if (error && !data) return <Failure error={error} retry={reload} />;
  const items = data?.items ?? [];
  const known = ["server", "pc", "phone", "tablet", "wearable", "router", "iot"];
  // Types outside the known list are shown under "other" rather than not at all.
  const typeOf = (c: Card) => (known.includes(c.device?.type ?? "") ? c.device!.type : "other");
  const groups = [...known, "other"]
    .map((t) => [t, items.filter((c) => typeOf(c) === t)] as const)
    .filter(([, l]) => l.length > 0);
  return (
    <>
      <Title h="设备" sub="服务器、电脑、手机和穿戴设备。没有接入采集的只显示手工登记的内容。"
        action={can("write") && <button className="primary" onClick={() => setAdding(true)}>登记设备</button>} />
      {data && items.length === 0 && <Empty title="这个空间还没有设备" hint="点“登记设备”添加第一台。" />}
      {groups.map(([t, list]) => (
        <Panel key={t} title={deviceTypeLabel[t] ?? t} note={`${list.length} 台`}>
          <div className="devices">
            {[...list].sort((a, b) => healthRank[a.health.state] - healthRank[b.health.state]).map((c) => <DeviceCard key={c.id} c={c} />)}
          </div>
        </Panel>
      ))}
      {adding && <EntityEditor kind="device" onClose={() => setAdding(false)} onSaved={reload} />}
    </>
  );
}

export function ServicesPage() {
  const { data, error, reload } = useCards("service");
  const [q, setQ] = useState("");
  const rows = useMemo(() => {
    const list = (data?.items ?? []).filter((c) => !q || c.name.includes(q) || c.slug.includes(q.toLowerCase()));
    return [...list].sort((a, b) => healthRank[a.health.state] - healthRank[b.health.state] || a.name.localeCompare(b.name, "zh"));
  }, [data, q]);
  if (error && !data) return <Failure error={error} retry={reload} />;
  return (
    <>
      <Title h="服务" sub="网站、接口、后台任务和基础设施。主机上的运行状态和外部探测分开判断。" />
      <div className="filters">
        <input type="search" placeholder="筛选服务" aria-label="筛选服务" value={q} onChange={(e) => setQ(e.target.value)} />
        <span>{rows.length} 个</span>
      </div>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr><th>服务</th><th>状态</th><th>运行于</th><th>项目</th><th>环境</th><th>最后检查</th></tr>
          </thead>
          <tbody>
            {rows.map((c) => (
              <tr key={c.id}>
                <td data-l="服务">
                  <a href={`#/e/${c.id}`}>{c.name}</a>{!c.confirmed && <> <Unconfirmed /></>}
                  {c.service?.url && <small>{c.service.url.replace("https://", "")}</small>}
                </td>
                <td data-l="状态"><HealthChip h={c.health} /></td>
                <td data-l="运行于">{c.service?.hosts.map((h) => h.restricted ? <span key="r" className="muted">受限资源</span> : <a key={h.id} href={`#/e/${h.id}`}>{h.name}</a>)}</td>
                <td data-l="项目">{c.service?.projects.map((p) => p.restricted ? null : <a key={p.id} href={`#/e/${p.id}`}>{p.name}</a>)}</td>
                <td data-l="环境">{c.service?.environment || "—"}</td>
                <td data-l="最后检查">{c.health.checked_at ? <Ago t={c.health.checked_at} /> : <span className="muted">未接入</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {data && rows.length === 0 && <Empty title="没有匹配的服务" />}
    </>
  );
}

export function ReposPage() {
  const { data, error, reload } = useCards("repository");
  const [only, setOnly] = useState<"all" | "local">("all");
  const rows = useMemo(() => {
    const list = (data?.items ?? []).filter((c) => only === "all" || (c.repo?.local_ahead ?? 0) + (c.repo?.local_dirty ?? 0) > 0);
    return [...list].sort((a, b) => (b.repo?.pushed_at ?? "").localeCompare(a.repo?.pushed_at ?? ""));
  }, [data, only]);
  if (error && !data) return <Failure error={error} retry={reload} />;
  const ciLabel: Record<string, string> = { ok: "通过", fail: "失败", unknown: "未完成" };
  return (
    <>
      <Title h="仓库" sub="云端状态来自 GitHub，本地状态来自各台电脑上的采集器。" />
      <div className="filters">
        <div className="seg" role="group" aria-label="范围">
          <button className={only === "all" ? "on" : ""} onClick={() => setOnly("all")}>全部</button>
          <button className={only === "local" ? "on" : ""} onClick={() => setOnly("local")}>本地有未同步</button>
        </div>
        <span>{rows.length} 个</span>
      </div>
      <div className="table-wrap">
        <table className="table">
          <thead>
            <tr><th>仓库</th><th>构建</th><th>本地</th><th>最近推送</th><th>PR / Issue</th><th>语言</th></tr>
          </thead>
          <tbody>
            {rows.map((c) => {
              const r = c.repo!;
              return (
                <tr key={c.id}>
                  <td data-l="仓库">
                    <a href={`#/e/${c.id}`}>{c.name}</a>
                    {r.visibility === "public" && <span className="tag">公开</span>}
                    {!c.confirmed && <> <Unconfirmed /></>}
                  </td>
                  <td data-l="构建">
                    {r.ci ? <span className="ci"><Lamp state={r.ci === "ok" ? "ok" : r.ci === "fail" ? "down" : "unknown"} size={12} />{ciLabel[r.ci] ?? r.ci}</span> : <span className="muted">无 CI</span>}
                  </td>
                  <td data-l="本地">
                    {r.local_ahead > 0 && <span className="tag tag-blocked">{r.local_ahead} 未推送</span>}
                    {r.local_dirty > 0 && <span className="tag">{r.local_dirty} 未提交</span>}
                    {r.local_ahead + r.local_dirty === 0 && <span className="muted">一致或无副本</span>}
                  </td>
                  <td data-l="最近推送">{r.pushed_at ? <Ago t={r.pushed_at} /> : "—"}</td>
                  <td data-l="PR / Issue" className="num">{r.open_prs ?? "—"} / {r.open_issues ?? "—"}</td>
                  <td data-l="语言">{r.language || "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {data && rows.length === 0 && <Empty title="没有需要同步的本地仓库" />}
    </>
  );
}
