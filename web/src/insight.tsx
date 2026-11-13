import { useMemo, useState } from "react";
import { useData } from "./lib";
import type { Diagnosis, LogLine, LogsOut, Playbook } from "./types";
import { Ago, Empty, Lamp, Panel } from "./ui";

// 日志终端和处理建议。日志只是“警告及以上”和失败单元的最后几行，保留 7 天，入库前已隐去疑似密钥。

const levelText: Record<LogLine["level"], string> = { critical: "严重", error: "错误", warning: "警告", notice: "提示", info: "信息" };

function clock(iso: string) {
  return new Date(iso).toLocaleString("zh-CN", { hour12: false, month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

export function LogTerminal({ id }: { id: string }) {
  const [level, setLevel] = useState("warning");
  const [since, setSince] = useState("24h");
  const [unit, setUnit] = useState("");
  const [q, setQ] = useState("");
  const path = `/api/v1/entities/${id}/logs?level=${level}&since=${since}&limit=300${unit ? `&unit=${encodeURIComponent(unit)}` : ""}${q.trim() ? `&q=${encodeURIComponent(q.trim())}` : ""}`;
  const { data, error } = useData<LogsOut>(path, 20000);
  const units = data?.units ?? [];
  const errs = units.reduce((n, u) => n + u.errors, 0);
  const warns = units.reduce((n, u) => n + u.warnings, 0);
  const max = Math.max(1, ...units.map((u) => u.errors + u.warnings));
  return (
    <Panel title="日志" className="logs-panel"
      note={data ? <><span className="mono">{errs}</span> 错误 · <span className="mono">{warns}</span> 警告</> : undefined}
      action={
        <div className="seg" role="group" aria-label="时间范围">
          {["1h", "6h", "24h", "72h", "168h"].map((s) => <button key={s} className={since === s ? "on" : ""} onClick={() => setSince(s)}>{s === "168h" ? "7d" : s}</button>)}
        </div>
      }>
      {error && !data ? <Empty title="日志读取失败" hint={error.message} /> : !data ? <div className="term term-loading">正在读取…</div> : (
        <>
          {!data.collecting && <p className="log-hint">这里还没有采集过日志。没有日志不代表没有问题：采集器要升级到带日志的版本，Linux 上还要能读系统日志。</p>}
          {units.length > 0 && (
            <div className="log-units">
              <button className={unit === "" ? "on" : ""} onClick={() => setUnit("")}>全部单元</button>
              {units.map((u) => (
                <button key={u.unit || "-"} className={unit === u.unit ? "on" : ""} onClick={() => setUnit(unit === u.unit ? "" : u.unit)} title={`最近一次 ${clock(u.newest)}`}>
                  <span className="lu-name">{u.unit || "系统"}</span>
                  <span className="lu-bar" aria-hidden="true">
                    <i className="lu-err" style={{ width: `${(u.errors / max) * 100}%` }} />
                    <i className="lu-warn" style={{ width: `${(u.warnings / max) * 100}%` }} />
                  </span>
                  <span className="mono lu-n">{u.errors > 0 && <b className="hot">{u.errors}</b>}{u.warnings > 0 && <b className="warm">{u.warnings}</b>}</span>
                </button>
              ))}
            </div>
          )}
          {data.text_shown ? (
            <>
              <div className="log-filter">
                <div className="seg" role="group" aria-label="级别">
                  {[["error", "错误"], ["warning", "警告及以上"], ["all", "全部"]].map(([k, l]) => <button key={k} className={level === k ? "on" : ""} onClick={() => setLevel(k)}>{l}</button>)}
                </div>
                <input type="search" placeholder="筛选日志内容" aria-label="筛选日志内容" value={q} onChange={(e) => setQ(e.target.value)} />
              </div>
              <div className="term" role="log">
                {data.lines.length === 0 ? <p className="term-empty">{data.collecting ? "这段时间没有符合条件的日志。" : "暂无数据"}</p> :
                  data.lines.map((l, i) => (
                    <div key={i} className={`tl tl-${l.level}`}>
                      <span className="tl-ts">{clock(l.ts)}</span>
                      <span className="tl-lv">{levelText[l.level]}</span>
                      <span className="tl-unit">{l.unit || l.ident || "system"}</span>
                      <span className="tl-msg">{l.message}{l.repeat ? <em> ×{l.repeat + 1}</em> : null}</span>
                    </div>
                  ))}
                <span className="term-cursor" aria-hidden="true" />
              </div>
            </>
          ) : data.withheld && <p className="log-hint">{data.withheld}</p>}
        </>
      )}
    </Panel>
  );
}

export function PlaybookView({ p }: { p: Playbook }) {
  const [copied, setCopied] = useState("");
  const copy = async (c: string) => {
    try {
      await navigator.clipboard.writeText(c);
      setCopied(c);
      setTimeout(() => setCopied(""), 1500);
    } catch {
      /* 剪贴板不可用时忽略 */
    }
  };
  return (
    <div className="playbook">
      <p className="pb-meaning">{p.meaning}</p>
      <ol className="pb-steps">{p.steps.map((s, i) => <li key={i}>{s}</li>)}</ol>
      {p.commands && p.commands.length > 0 && (
        <div className="pb-cmds">
          <span className="pb-label">只读排查命令</span>
          {p.commands.map((c) => (
            <button key={c} className="pb-cmd" onClick={() => copy(c)} title="点击复制">
              <code>{c}</code><span>{copied === c ? "已复制" : "复制"}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// 对象详情页上的“诊断”：每个未解决的问题配上含义和处理步骤，再列最近的探测结果。
export function DiagnosisPanel({ id }: { id: string }) {
  const { data } = useData<Diagnosis>(`/api/v1/entities/${id}/diagnose`, 30000);
  const [open, setOpen] = useState<string | null>(null);
  const probes = data?.probes ?? [];
  if (!data || (data.problems.length === 0 && probes.length === 0)) return null;
  return (
    <Panel title="诊断" className="diag-panel" note="每个问题的含义和处理步骤，以及最近的探测记录">
      {data.problems.length > 0 && (
        <ul className="diag-list">
          {data.problems.map((p) => (
            <li key={p.id} className={`diag sev-${p.severity}`}>
              <button className="diag-head" onClick={() => setOpen(open === p.id ? null : p.id)} aria-expanded={open === p.id}>
                <Lamp state={p.severity === "critical" ? "down" : p.severity === "warning" ? "degraded" : "unknown"} size={13} />
                <span className="diag-title">{p.title}</span>
                <Ago t={p.first_seen} prefix="发现于 " />
                <span className="diag-chev" aria-hidden="true">{open === p.id ? "−" : "+"}</span>
              </button>
              {open === p.id && (
                <div className="diag-body">
                  {p.detail && <p className="diag-detail">{p.detail}</p>}
                  {p.playbook ? <PlaybookView p={p.playbook} /> : p.action && <p>{p.action}</p>}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {probes.length > 0 && (
        <div className="probe-strips">
          {probes.map((pr) => (
            <div className="probe-strip" key={pr.kind + pr.target}>
              <span className="mono ps-kind">{pr.kind}</span>
              <span className="ps-target" title={pr.target}>{pr.target}</span>
              <span className="ps-runs" aria-label={`最近 ${pr.runs.length} 次探测`}>
                {[...pr.runs].reverse().map((r, i) => (
                  <i key={i} className={r.ok ? "ok" : "bad"} title={`${clock(r.ts)} ${r.ok ? "成功" : "失败"}${r.latency_ms != null ? ` ${r.latency_ms}ms` : ""}${r.error ? ` · ${r.error}` : ""}`} />
                ))}
              </span>
              <span className="mono ps-lat">{pr.runs[0]?.latency_ms != null ? `${pr.runs[0].latency_ms}ms` : "—"}</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );
}

// 总览待处理里的“怎么处理”：展开时才加载对象的诊断，取这条规则的处理步骤和最近的错误日志。
export function FindingHelp({ entityId, rule }: { entityId: string; rule: string }) {
  const { data, error } = useData<Diagnosis>(`/api/v1/entities/${entityId}/diagnose`);
  const p = useMemo(() => data?.problems.find((x) => x.rule === rule)?.playbook, [data, rule]);
  if (error) return <p className="log-hint">{error.message}</p>;
  if (!data) return <p className="log-hint">正在读取处理建议…</p>;
  const errs = data.logs?.lines.filter((l) => l.priority <= 3).slice(0, 3) ?? [];
  return (
    <div className="finding-help">
      {p ? <PlaybookView p={p} /> : <p className="log-hint">这条规则还没有处理步骤。</p>}
      {errs.length > 0 && (
        <div className="term term-mini">
          {errs.map((l, i) => (
            <div key={i} className={`tl tl-${l.level}`}><span className="tl-ts">{clock(l.ts)}</span><span className="tl-unit">{l.unit || l.ident}</span><span className="tl-msg">{l.message}</span></div>
          ))}
        </div>
      )}
    </div>
  );
}
