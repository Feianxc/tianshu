import { useState, type ReactNode } from "react";
import { useApp } from "../app";
import { api, collectorLabel, deviceTypeLabel, freshLabel, healthLabel, kindLabel, relationLabel, storageLabel, str, uptime, useData } from "../lib";
import type { Detail, Ref, Sample, Signal } from "../types";
import { Ago, Drawer, Empty, Failure, HealthChip, Lamp, Panel, StageRail, TrendChart, Unconfirmed } from "../ui";
import { EntityEditor } from "./Editor";
import { DiagnosisPanel, LogTerminal } from "../insight";

const lifeLabel: Record<string, string> = { active: "运行中", paused: "已暂停", planned: "计划中", retired: "已退役" };

const attrLabel: Record<string, string> = {
  provider: "提供方", os: "系统", address: "地址", ssh_alias: "SSH 别名", ssh_port: "SSH 端口", availability: "在线规律",
  service_kind: "类型", environment: "环境", unit: "运行单元", units: "运行单元", url: "访问地址", role: "我的角色", goal: "目标",
  due: "计划日期", local_path: "本地目录", storage: "存放方式", mode: "使用方式", locator: "存放位置", usage: "用途", rotation_due: "计划轮换",
  full_name: "仓库", visibility: "可见性", default_branch: "默认分支", language: "语言",
};

const availLabel: Record<string, string> = { always_on: "一直在线", intermittent: "间歇在线", event: "事件上报", manual: "手工登记" };

function AttrValue({ k, v }: { k: string; v: unknown }) {
  if (Array.isArray(v)) return <>{v.map(String).join("、")}</>;
  const s = String(v);
  if (k === "url" && s.startsWith("http")) return <a href={s} target="_blank" rel="noreferrer">{s}</a>;
  if (k === "availability") return <>{availLabel[s] ?? s}</>;
  if (k === "storage") return <>{storageLabel[s] ?? s}</>;
  return <>{s}</>;
}

function Attrs({ d }: { d: Detail }) {
  const rows = Object.entries(d.attrs).filter(([k]) => attrLabel[k]);
  if (rows.length === 0) return null;
  return (
    <dl className="attrs">
      {rows.map(([k, v]) => (
        <div key={k}><dt>{attrLabel[k]}</dt><dd><AttrValue k={k} v={v} /></dd></div>
      ))}
    </dl>
  );
}

function Relations({ refs }: { refs: Ref[] }) {
  const groups = new Map<string, Ref[]>();
  for (const r of refs) {
    const label = `${relationLabel[r.relation]?.[r.direction === "out" ? 0 : 1] ?? r.relation}的${kindLabel[r.kind]}`;
    groups.set(label, [...(groups.get(label) ?? []), r]);
  }
  if (groups.size === 0) return <Empty title="还没有关联" hint="在目录文件里用 runs_on、project、repo 等字段建立关系。" />;
  return (
    <div className="rels">
      {[...groups.entries()].map(([label, list]) => (
        <div key={label}>
          <h3>{label}</h3>
          <ul>
            {list.map((r, i) => (
              <li key={r.id || i}>
                <Lamp state={r.health} size={12} />
                {r.restricted ? <span className="muted">受限资源</span> : <a href={`#/e/${r.id}`}>{r.name}</a>}
              </li>
            ))}
          </ul>
        </div>
      ))}
    </div>
  );
}

const signalName = (s: string) =>
  s.replace(/^host\.heartbeat$/, "心跳").replace(/^host\.cpu$/, "CPU").replace(/^host\.memory$/, "内存").replace(/^host\.systemd$/, "系统服务")
    .replace(/^host\.battery$/, "电量").replace(/^host\.disk:/, "磁盘 ").replace(/^unit:[^:]+:/, "单元 ").replace(/^check\.(http|tcp|tls):/, "$1 探测 ")
    .replace(/^repo\.meta$/, "仓库同步").replace(/^repo\.ci$/, "构建").replace(/^repo\.local:/, "本地副本 ").replace(/^sec\.ssh$/, "登录入口");

const rootLabel: Record<string, string> = {
  yes: "允许，包括密码", "prohibit-password": "只能用钥匙", "without-password": "只能用钥匙", "forced-commands-only": "只能执行指定命令", no: "禁止",
};

// 登录入口是暴露面，不是健康：开着密码登录的主机照样运行正常，所以单独放一块，不进灯色。
function LoginPosture({ g }: { g: Signal }) {
  const v = g.value as { password_auth?: string; root_login?: string; ports?: number[]; fail2ban?: string; failed_last_hour?: number; keys?: Record<string, number> };
  const keys = Object.entries(v.keys ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <Panel title="登录入口" note="SSH 实际生效的配置，随采集读取。这是暴露面，不计入健康状态" action={<Ago t={g.received_at} prefix="读取于 " />}>
      <dl className="posture">
        <div className={v.password_auth === "yes" ? "hot" : ""}><dt>密码登录</dt><dd>{v.password_auth === "yes" ? "开着" : v.password_auth === "no" ? "已关闭" : "读不到"}</dd></div>
        <div className={v.root_login === "yes" && v.password_auth === "yes" ? "hot" : ""}><dt>root 登录</dt><dd>{v.root_login === "yes" && v.password_auth === "no" ? "允许，只能用钥匙" : rootLabel[v.root_login ?? ""] ?? "读不到"}</dd></div>
        <div><dt>端口</dt><dd>{v.ports?.length ? v.ports.join("、") : "读不到"}</dd></div>
        <div><dt>过去 1 小时失败登录</dt><dd className="num">{v.failed_last_hour != null ? v.failed_last_hour.toLocaleString("zh-CN") : "读不到"}</dd></div>
        <div className={v.fail2ban === "active" ? "" : "hot"}><dt>fail2ban</dt><dd>{v.fail2ban === "active" ? "运行中" : v.fail2ban ? "没有运行" : "读不到"}</dd></div>
      </dl>
      <h3 className="sub">能登录的钥匙 {keys.length} 把</h3>
      {keys.length === 0 ? <p className="muted">读不到钥匙文件。</p> : (
        <ul className="keys">
          {keys.map(([k, t]) => (
            <li key={k} className={t > 0 ? "hot" : ""}>
              <code title={k}>{k.slice(0, 19)}…</code>
              <span>{t > 0 ? <>新增于 <Ago t={new Date(t).toISOString()} /></> : "开始观察时已有"}</span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}

function Signals({ list }: { list: Signal[] }) {
  if (list.length === 0) return <Empty title="没有信号" hint="这个对象还没有接入任何数据来源，所以状态是未知。" />;
  return (
    <div className="table-wrap">
      <table className="table table-tight">
        <thead><tr><th>信号</th><th>结果</th><th>来源</th><th>观测时间</th></tr></thead>
        <tbody>
          {list.map((g) => (
            <tr key={g.signal} className={g.stale ? "row-stale" : ""}>
              <td data-l="信号"><span className="sig-state"><Lamp state={g.stale ? "unknown" : g.state === "ok" ? "ok" : g.state === "fail" ? "down" : g.state === "warn" ? "degraded" : "unknown"} size={12} />{signalName(g.signal)}</span></td>
              <td data-l="结果">{g.summary}{Array.isArray(g.value.failed) && g.value.failed.length > 0 && <small>{(g.value.failed as string[]).join("、")}</small>}{g.error && g.error !== g.summary && <small>{g.error}</small>}</td>
              <td data-l="来源">{g.source}</td>
              <td data-l="观测时间"><Ago t={g.received_at} />{g.stale && <span className="tag">已过期</span>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Metrics({ id }: { id: string }) {
  const [range, setRange] = useState("24h");
  const { data } = useData<{ from: string; to: string; items: Sample[] }>(`/api/v1/entities/${id}/metrics?range=${range}`, 60000);
  return (
    <Panel title="资源趋势" note="原始数据保留 30 天" action={
      <div className="seg" role="group" aria-label="时间范围">
        {["1h", "6h", "24h", "7d", "30d"].map((r) => <button key={r} className={range === r ? "on" : ""} onClick={() => setRange(r)}>{r}</button>)}
      </div>
    }>
      {data && (
        <div className="trends">
          <TrendChart title="CPU" field="cpu_pct" items={data.items} from={data.from} to={data.to} />
          <TrendChart title="内存" field="mem_pct" items={data.items} from={data.from} to={data.to} />
          <TrendChart title="磁盘" field="disk_pct" items={data.items} from={data.from} to={data.to} />
        </div>
      )}
    </Panel>
  );
}

function EvidenceForm({ id, onClose, onSaved }: { id: string; onClose: () => void; onSaved: () => void }) {
  const [verdict, setVerdict] = useState("pass");
  const [scope, setScope] = useState("");
  const [note, setNote] = useState("");
  const [err, setErr] = useState("");
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await api.post(`/api/v1/entities/${id}/evidence`, { verdict, scope, note });
      onSaved();
      onClose();
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  return (
    <Drawer title="记录一次验证" onClose={onClose}>
      <form className="form" onSubmit={save}>
        <label><span>结论</span>
          <select value={verdict} onChange={(e) => setVerdict(e.target.value)}>
            {["pass", "fail", "partial", "blocked", "not run"].map((v) => <option key={v}>{v}</option>)}
          </select>
        </label>
        <label><span>实际验证了什么</span><textarea rows={2} value={scope} onChange={(e) => setScope(e.target.value)} required /></label>
        <label><span>说明</span><textarea rows={2} value={note} onChange={(e) => setNote(e.target.value)} placeholder="blocked 和 not run 必须写原因" /></label>
        {err && <p className="form-error" role="alert">{err}</p>}
        <footer><button className="primary">保存</button><button type="button" className="ghost" onClick={onClose}>取消</button></footer>
      </form>
    </Drawer>
  );
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return <div className="fact"><span>{label}</span><b>{children}</b></div>;
}

export function DetailPage({ id }: { id: string }) {
  const { can, spaceName } = useApp();
  const { data: d, error, reload } = useData<Detail>(`/api/v1/entities/${id}`, 20000);
  const [editing, setEditing] = useState(false);
  const [evidence, setEvidence] = useState(false);
  if (error && !d) return <Failure error={error} retry={reload} />;
  if (!d) return <div className="loading">读取中</div>;
  const confirm = async () => {
    await api.patch(`/api/v1/entities/${id}`, { confirmed: true, rev: d.rev });
    reload();
  };
  const p = d.project, dev = d.device;
  return (
    <>
      <header className="detail-head">
        <p className="crumb"><span>{kindLabel[d.kind]}</span><i>/</i><span>{spaceName(d.workspace)}</span><i>/</i><span>{lifeLabel[d.lifecycle]}</span></p>
        <div className="detail-title">
          <h1>{d.name}</h1>
          {d.kind !== "credential" && <HealthChip h={d.health} />}
        </div>
        {d.summary && <p className="detail-sum">{d.summary}</p>}
        {d.kind !== "credential" && d.health.reason && <p className="detail-reason">{d.health.reason}{d.health.freshness !== "fresh" && `（${freshLabel[d.health.freshness]}）`}</p>}
        {can("write") && (
          <div className="detail-actions">
            <button onClick={() => setEditing(true)}>编辑</button>
            {!d.confirmed && <button className="primary" onClick={confirm}>确认归属</button>}
            {d.kind === "project" && can("evidence") && <button onClick={() => setEvidence(true)}>记录验证</button>}
          </div>
        )}
        {!d.confirmed && <p className="pending-note"><Unconfirmed /> 这条信息是推断的，确认后才算数。</p>}
      </header>

      {p && (
        <section className="facts">
          <Fact label="阶段"><StageRail stage={p.stage} /></Fact>
          <Fact label="优先级">{p.priority || "未定"}</Fact>
          <Fact label="运行服务">{p.services > 0 ? `${p.services_up}/${p.services} 正常` : "无"}</Fact>
          <Fact label="最近活动">{p.last_activity ? <Ago t={p.last_activity} /> : "—"}</Fact>
        </section>
      )}
      {p && (
        <Panel title="下一步">
          <p className={p.next_step ? "next-step" : "next-step muted"}>{p.next_step || "还没有写下一步"}</p>
          {Array.isArray(d.attrs.blockers) && d.attrs.blockers.length > 0 && (
            <ul className="blockers">
              {(d.attrs.blockers as { text?: string; owner?: string }[]).map((b, i) => (
                <li key={i}><Lamp state="degraded" size={12} />{b.text ?? String(b)}{b.owner && <small>负责人 {b.owner}</small>}</li>
              ))}
            </ul>
          )}
        </Panel>
      )}
      {dev && (
        <section className="facts">
          <Fact label="类型">{deviceTypeLabel[dev.type] ?? "设备"}</Fact>
          <Fact label="系统">{dev.os || "未知"}</Fact>
          <Fact label="已运行">{uptime(dev.uptime_s)}</Fact>
          <Fact label="采集方式">{dev.collector ? (collectorLabel[dev.collector] ?? dev.collector) : "未接入"}</Fact>
        </section>
      )}

      {d.open_findings.length > 0 && (
        <Panel title="待处理问题">
          <ul className="findings">
            {d.open_findings.map((f) => (
              <li key={f.id} className={`finding sev-${f.severity}`}>
                <Lamp state={f.severity === "critical" ? "down" : f.severity === "warning" ? "degraded" : "unknown"} size={14} />
                <div className="finding-body">
                  <strong>{f.title}</strong>
                  <p>{f.detail}</p>
                  {f.action && <span className="finding-action">{f.action}</span>}
                </div>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      {(d.kind === "device" || d.kind === "service") && <DiagnosisPanel id={d.id} />}
      {d.kind === "device" && dev?.last_seen && <Metrics id={d.id} />}
      {(d.kind === "device" || d.kind === "service") && <LogTerminal id={d.id} />}
      {d.kind === "device" && d.signals.filter((g) => g.signal === "sec.ssh").map((g) => <LoginPosture key={g.signal} g={g} />)}

      <div className="cols cols-even">
        <Panel title="关联"><Relations refs={d.relations} /></Panel>
        <Panel title="详情">
          {Object.keys(d.attrs).some((k) => attrLabel[k]) ? <Attrs d={d} /> : <Empty title="没有更多信息" />}
          {d.tags.length > 0 && <div className="tags">{d.tags.map((t) => <span className="tag" key={t}>{t}</span>)}</div>}
        </Panel>
      </div>

      {(d.kind === "device" || d.kind === "service" || d.kind === "repository") && (
        <Panel title="信号" note="每条信号都带来源和时间，过期的不再算当前状态"><Signals list={d.signals} /></Panel>
      )}

      {d.kind === "credential" && (
        <Panel title="这里只有引用">
          <p className="note">天枢只记录这个凭据存在哪、用在哪，不保存密码或私钥本身。</p>
        </Panel>
      )}

      {d.checks.length > 0 && (
        <Panel title="探测" note="探测只说明地址有响应，不代表业务流程通过">
          <ul className="checks">
            {d.checks.map((c) => <li key={c.id}><b>{c.kind}</b><span>{c.target}</span><small>每 {c.interval_s >= 3600 ? `${c.interval_s / 3600} 小时` : `${c.interval_s} 秒`}</small></li>)}
          </ul>
        </Panel>
      )}

      {d.kind === "project" && (
        <Panel title="验收记录" note="Agent 提交的是声明，不会改变项目阶段">
          {d.evidence.length === 0 ? (
            <Empty title="还没有验证记录" hint="pass、fail、partial、blocked、not run 分别记录，运行状态正常不等于业务验收通过。" />
          ) : (
            <ul className="evidence">
              {d.evidence.map((e) => (
                <li key={e.id}>
                  <b className={`verdict v-${e.verdict.replace(" ", "-")}`}>{e.verdict}</b>
                  <div>
                    <p>{e.scope}</p>
                    {e.note && <small>{e.note}</small>}
                    <small>{e.by_kind === "agent" ? "Agent" : "本人"} {e.by_name} · <Ago t={e.created_at} /></small>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      )}

      {str(d.origin) && <p className="foot-note">来源：{d.origin === "catalog" ? "目录文件" : d.origin === "github" ? "GitHub 同步" : "手工录入"} · 版本 {d.rev} · 规则版本 {d.health.rule_version} · {healthLabel[d.health.state]}</p>}

      {editing && <EntityEditor kind={d.kind} entity={d} onClose={() => setEditing(false)} onSaved={reload} />}
      {evidence && <EvidenceForm id={d.id} onClose={() => setEvidence(false)} onSaved={reload} />}
    </>
  );
}

