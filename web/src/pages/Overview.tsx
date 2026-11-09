import { useState } from "react";
import { hms, useApp } from "../app";
import { api, deviceTypeLabel, sourceCondition, uptime } from "../lib";
import type { Card, Finding, Overview } from "../types";
import { Ago, DeviceIcon, Empty, Failure, HealthChip, Lamp, Meter, Panel, StageRail, Unconfirmed } from "../ui";
import { Telemetry } from "../telemetry";
import { StarLegend, StarMap } from "../starmap";

// 首屏的论点：一句话说清现在要不要你出手。数字只在需要你处理时着色。
function headline(o: Overview): { n?: number; tone: string; text: string; sub: string } {
  const crit = o.attention.filter((f) => f.severity === "critical").length;
  const warn = o.attention.length - crit;
  const lamps = o.wall.flatMap((r) => [...r.devices, ...r.services]);
  const blind = lamps.filter((l) => l.state === "unknown").length;
  if (crit > 0) return { n: crit, tone: "hot", text: " 件事需要马上处理", sub: warn > 0 ? `另有 ${warn} 件等你处理。` : "" };
  if (warn > 0) return { n: warn, tone: "warm", text: " 件事等你处理", sub: "" };
  if (blind > 0) return { tone: "", text: `看得见的都正常，还有 ${blind} 处看不见`, sub: "没有证据的地方不算正常。" };
  return { tone: "", text: "平安无事", sub: "" };
}

function FindingRow({ f, reload }: { f: Finding; reload: () => void }) {
  const { can } = useApp();
  // 长期存在、短期解决不了的事项（比如明文密钥等密匣迁移）可以推迟到一周或一个月后再提醒。
  const snooze = async (hours: number) => {
    await api.post(`/api/v1/findings/${f.id}/ack`, { snooze_hours: hours });
    reload();
  };
  return (
    <li className={`finding sev-${f.severity}`}>
      <Lamp state={f.severity === "critical" ? "down" : f.severity === "warning" ? "degraded" : "unknown"} size={13} />
      <div className="finding-body">
        <a className="finding-title" href={`#/e/${f.entity_id}`}>{f.title}</a>
        <p>{f.detail}</p>
        <div className="finding-foot">
          {f.action && <span className="finding-action">{f.action}</span>}
          <Ago t={f.first_seen} prefix="发现于 " />
          {can("write") && (
            <select className="snooze" aria-label={`稍后提醒：${f.title}`} value="" onChange={(e) => e.target.value && snooze(Number(e.target.value))}>
              <option value="" disabled hidden>稍后提醒</option>
              <option value="24">明天再提醒</option>
              <option value="168">一周后再提醒</option>
              <option value="720">一个月后再提醒</option>
            </select>
          )}
        </div>
      </div>
    </li>
  );
}

// 紧急的全部展开；其余先给 4 条，剩下的收起来，免得一长串把别的内容挤下去。
function Findings({ list, reload }: { list: Finding[]; reload: () => void }) {
  const [all, setAll] = useState(false);
  const crit = list.filter((f) => f.severity === "critical");
  const rest = list.filter((f) => f.severity !== "critical");
  const shown = all ? list : [...crit, ...rest.slice(0, 4)];
  const hidden = list.length - shown.length;
  return (
    <>
      <ul className="findings">{shown.map((f) => <FindingRow key={f.id} f={f} reload={reload} />)}</ul>
      {hidden > 0 && <button className="more" onClick={() => setAll(true)}>展开其余 {hidden} 项</button>}
      {all && rest.length > 4 && <button className="more" onClick={() => setAll(false)}>收起</button>}
    </>
  );
}

export function DeviceCard({ c }: { c: Card }) {
  const d = c.device!;
  return (
    <a className={`device ${c.worst_severity === "critical" ? "device-hot" : ""}`} href={`#/e/${c.id}`}>
      <div className="device-head">
        <DeviceIcon type={d.type} />
        <strong>{c.name}</strong>
        {!c.confirmed && <Unconfirmed />}
      </div>
      <HealthChip h={c.health} />
      {d.last_seen ? (
        <div className="device-meters">
          <Meter label="CPU" value={d.cpu_pct} warn={90} fail={101} />
          <Meter label="内存" value={d.mem_pct} warn={90} fail={97} />
          <Meter label="磁盘" value={d.disk_pct} note={d.disk_free_gb != null ? `余 ${d.disk_free_gb.toFixed(0)} GB` : undefined} />
        </div>
      ) : (
        <p className="device-reason">{c.health.reason}</p>
      )}
      <div className="device-foot">
        <span>{deviceTypeLabel[d.type] ?? "设备"}</span>
        {d.services > 0 && <span>{d.services} 个服务</span>}
        {d.uptime_s != null && <span>已运行 {uptime(d.uptime_s)}</span>}
        {d.last_seen && <Ago t={d.last_seen} />}
      </div>
    </a>
  );
}

function ProjectLine({ c }: { c: Card }) {
  const p = c.project!;
  return (
    <a className={`pline ${p.update_stale ? "pline-quiet" : ""}`} href={`#/e/${c.id}`}>
      <span className={`pri pri-${p.priority}`}>{p.priority || "—"}</span>
      <span className="pline-main">
        <strong>{c.name}</strong>
        <span className={p.next_step ? "pline-next" : "pline-next pline-missing"}>{p.next_step || "没有写下一步"}</span>
      </span>
      <StageRail stage={p.stage} />
      {p.blockers > 0 && <span className="tag tag-blocked">阻塞 {p.blockers}</span>}
    </a>
  );
}

export function OverviewPage() {
  const { ov, space, spaceName } = useApp();
  const { data: o, error, reload } = ov;
  if (error && !o) return <Failure error={error} retry={reload} />;
  if (!o) return <div className="loading">正在对账</div>;
  const h = headline(o);
  const focus = o.projects.filter((c) => ["building", "verifying", "planned"].includes(c.project?.stage ?? ""));
  const badSources = o.sources.filter((s) => s.condition !== "ok");
  const today = new Date().toLocaleDateString("zh-CN", { month: "long", day: "numeric", weekday: "long" });
  return (
    <>
      <header className="hero">
        <div className="hero-main">
          <p className="hero-eyebrow"><span>{space ? spaceName(space) : "全部空间"}</span><span>{today}</span></p>
          <h1>{h.n != null && <span className={h.tone}>{h.n}</span>}{h.text}</h1>
          <p className="hero-sub">
            {h.sub && <>{h.sub} </>}
            共 <span className="mono">{o.totals.device ?? 0}</span> 台设备、<span className="mono">{o.totals.service ?? 0}</span> 个服务、
            <span className="mono">{o.totals.project ?? 0}</span> 个项目、<span className="mono">{o.totals.repository ?? 0}</span> 个仓库。
            {error && <span className="warn-inline"> 刷新失败，显示的是上一次的数据。</span>}
          </p>
          <div className="recon" title="天枢最近一次汇总各来源数据的时间，每 15 秒刷新">
            <span className="recon-label">最近对账</span>
            <span className="recon-time">{hms(o.generated_at)}</span>
            <Ago t={o.generated_at} />
          </div>
          <div className="cycle" key={o.generated_at} aria-hidden="true"><i /></div>
          <StarLegend rows={o.wall} />
        </div>
        <StarMap rows={o.wall} devices={o.devices} generatedAt={o.generated_at} />
      </header>

      <Telemetry devices={o.devices} />

      <div className="cols ov-main">
        <Panel title="需要处理" count={o.attention.length || undefined} note={o.snoozed > 0 ? `${o.snoozed} 项已稍后提醒` : undefined}>
          {o.attention.length === 0 ? (
            <Empty title="现在没有需要你处理的事" hint="有问题时会出现在这里。" />
          ) : (
            <Findings list={o.attention} reload={reload} />
          )}
        </Panel>
        <div className="stack">
          <Panel title="进行中的项目" count={focus.length || undefined} action={<a href="#/projects">全部项目</a>}>
            {focus.length === 0 ? <Empty title="没有进行中的项目" /> : <div className="plines">{focus.map((c) => <ProjectLine key={c.id} c={c} />)}</div>}
          </Panel>
          <Panel title="临近到期">
            {o.expiring.length === 0 ? (
              <Empty title="45 天内没有到期事项" hint="只统计已接入探测的证书和已填写日期的事项。" />
            ) : (
              <ul className="list">
                {o.expiring.map((e, i) => (
                  <li key={i}>
                    <a href={`#/e/${e.entity_id}`}>{e.name}</a>
                    <span>{e.what}</span>
                    <b className={`mono ${e.days_left < 14 ? "days-hot" : ""}`}>{e.days_left} 天</b>
                  </li>
                ))}
              </ul>
            )}
          </Panel>
          <Panel title="数据来源" count={o.sources.length || undefined} action={<a href="#/sources">接入详情</a>}>
            {o.sources.length === 0 ? <Empty title="还没有接入数据来源" /> : badSources.length === 0 ? (
              <p className="sources-ok"><Lamp state="ok" size={11} />{o.sources.length} 个来源都在按时上报。</p>
            ) : (
              <ul className="list">
                {badSources.map((s) => (
                  <li key={s.name}><span>{s.name}</span><span>{s.last_error}</span><b className="days-hot">{sourceCondition[s.condition]}</b></li>
                ))}
              </ul>
            )}
          </Panel>
        </div>
      </div>

      <Panel title="设备" count={o.devices.length || undefined} action={<a href="#/devices">全部设备</a>}>
        {o.devices.length === 0 ? <Empty title="这个空间没有设备" /> : <div className="devices">{o.devices.map((c) => <DeviceCard key={c.id} c={c} />)}</div>}
      </Panel>

      {o.gaps.length > 0 && (
        <Panel title="覆盖缺口" count={o.gaps.length} note="不紧急，但这些地方出问题时你看不到">
          <ul className="gaps">
            {o.gaps.map((f) => (
              <li key={f.id}><a href={`#/e/${f.entity_id}`}>{f.title}</a><span>{f.action}</span></li>
            ))}
          </ul>
        </Panel>
      )}
    </>
  );
}
