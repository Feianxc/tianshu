import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { ago, freshLabel, fullTime, healthLabel, stageLabel, stageOrder, useBackToClose, useTick } from "./lib";
import type { Freshness, Health, HealthState, Sample } from "./types";

// 灯。形状和颜色同时编码状态，色盲和灰度打印也读得出来：
// 圆是正常，三角是降级，菱形是故障，空心是没有证据，虚线是有意暂停。
export function Lamp({ state, freshness, size = 14 }: { state: HealthState; freshness?: Freshness; size?: number }) {
  const blind = freshness === "stale" && (state === "ok" || state === "unknown");
  let shape: ReactNode;
  if (blind || state === "unknown") {
    shape = <circle cx="7" cy="7" r="4.6" className="lamp-ring" />;
  } else if (state === "ok") {
    shape = <circle cx="7" cy="7" r="5.2" className="lamp-ok" />;
  } else if (state === "degraded") {
    shape = <path d="M7 1.6 12.8 11.8H1.2Z" className="lamp-warn" />;
  } else if (state === "down") {
    shape = <path d="M7 .8 13.2 7 7 13.2.8 7Z" className="lamp-fail" />;
  } else if (state === "paused") {
    shape = <circle cx="7" cy="7" r="4.6" className="lamp-dash" />;
  } else if (state === "offline") {
    shape = <circle cx="7" cy="7" r="3.4" className="lamp-off" />;
  } else {
    shape = <circle cx="7" cy="7" r="1.8" className="lamp-off" />;
  }
  return (
    <svg className="lamp" width={size} height={size} viewBox="0 0 14 14" aria-hidden="true">
      {shape}
    </svg>
  );
}

export function HealthChip({ h, reason = false }: { h: Health; reason?: boolean }) {
  const stale = h.freshness === "stale" || h.freshness === "partial";
  return (
    <span className={`chip chip-${h.state}`} title={h.reason}>
      <Lamp state={h.state} freshness={h.freshness} size={12} />
      <span>{healthLabel[h.state]}</span>
      {stale && h.state !== "paused" && <span className="chip-note">{freshLabel[h.freshness]}</span>}
      {reason && h.reason && <span className="chip-reason">{h.reason}</span>}
    </span>
  );
}

export function Ago({ t, prefix }: { t: string | null | undefined; prefix?: string }) {
  useTick();
  return (
    <time className="ago" dateTime={t ?? undefined} title={fullTime(t)}>
      {prefix}
      {ago(t)}
    </time>
  );
}

// 资源条。没测到时显示“未测”，绝不画成 0。
export function Meter({ label, value, warn = 85, fail = 92, note }: { label: string; value: number | null | undefined; warn?: number; fail?: number; note?: string }) {
  const level = value == null ? "none" : value >= fail ? "fail" : value >= warn ? "warn" : "ok";
  return (
    <div className={`meter meter-${level}`}>
      <span className="meter-label">{label}</span>
      <span className="meter-track" aria-hidden="true">
        {value != null && <span className="meter-fill" style={{ width: `${Math.min(100, Math.max(2, value))}%` }} />}
      </span>
      <span className="meter-value">{value == null ? "未测" : `${Math.round(value)}%`}</span>
      {note && <span className="meter-note">{note}</span>}
    </div>
  );
}

export function StageRail({ stage }: { stage: string }) {
  const i = stageOrder.indexOf(stage);
  if (i < 0) return <span className="stage-off">{stageLabel[stage] ?? "未定"}</span>;
  return (
    <span className="rail-stage" title={stageLabel[stage]}>
      <span className="rail-ticks" aria-hidden="true">
        {stageOrder.map((s, n) => (
          <span key={s} className={n < i ? "tick done" : n === i ? "tick now" : "tick"} />
        ))}
      </span>
      <span className="rail-label">{stageLabel[stage]}</span>
    </span>
  );
}

export function Panel({ title, count, note, action, children, id, className }: { title: string; count?: number; note?: ReactNode; action?: ReactNode; children: ReactNode; id?: string; className?: string }) {
  return (
    <section className={className ? `panel ${className}` : "panel"} id={id}>
      <header className="panel-head">
        <h2>{title}</h2>
        {count != null && <span className="n mono">{count}</span>}
        {note && <span className="panel-note">{note}</span>}
        {action && <span className="panel-action">{action}</span>}
      </header>
      {children}
    </section>
  );
}

export function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="empty">
      <strong>{title}</strong>
      {hint && <p>{hint}</p>}
    </div>
  );
}

export function Failure({ error, retry }: { error: Error; retry?: () => void }) {
  return (
    <div className="failure" role="alert">
      <strong>读取失败</strong>
      <p>{error.message}</p>
      {retry && <button onClick={retry}>重试</button>}
    </div>
  );
}

export function Unconfirmed() {
  return <span className="tag tag-pending" title="这是推断出来的，等你确认">待确认</span>;
}

export function Drawer({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useBackToClose(true, onClose);
  useEffect(() => {
    const f = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("keydown", f);
    ref.current?.querySelector<HTMLElement>("input,textarea,select")?.focus();
    return () => removeEventListener("keydown", f);
  }, [onClose]);
  return (
    <div className="drawer-scrim" onClick={onClose}>
      <div className="drawer" role="dialog" aria-modal="true" aria-label={title} ref={ref} onClick={(e) => e.stopPropagation()}>
        <header>
          <h2>{title}</h2>
          <button className="ghost" onClick={onClose}>关闭</button>
        </header>
        {children}
      </div>
    </div>
  );
}

// 单序列趋势图：Y 轴固定 0–100，缺数据处断线，不把空隙连起来。
type Series = "cpu_pct" | "mem_pct" | "disk_pct";

export function TrendChart({ title, field, items, from, to, unit = "%" }: { title: string; field: Series; items: Sample[]; from: string; to: string; unit?: string }) {
  const uid = useId();
  const W = 640, H = 132, L = 30, R = 8, T = 10, B = 20;
  const t0 = new Date(from).getTime(), t1 = new Date(to).getTime();
  const x = (t: number) => L + ((t - t0) / (t1 - t0)) * (W - L - R);
  const y = (v: number) => T + (1 - v / 100) * (H - T - B);
  const pts = useMemo(() => items.filter((s) => s[field] != null).map((s) => ({ t: new Date(s.ts).getTime(), v: s[field] as number })), [items, field]);
  const gap = pts.length > 2 ? median(pts.slice(1).map((p, i) => p.t - pts[i].t)) * 4 : Infinity;
  const path = pts.map((p, i) => `${i === 0 || p.t - pts[i - 1].t > gap ? "M" : "L"}${x(p.t).toFixed(1)} ${y(p.v).toFixed(1)}`).join("");
  // 面积只铺在连续段下面，断开的地方不填。
  const area = path.split("M").filter(Boolean).map((seg) => {
    const pairs = seg.split("L").map((s) => s.trim().split(" "));
    const first = pairs[0], last = pairs[pairs.length - 1];
    return `M${first[0]} ${y(0)}L${seg}L${last[0]} ${y(0)}Z`;
  }).join("");
  const [hover, setHover] = useState<{ t: number; v: number } | null>(null);
  const svg = useRef<SVGSVGElement>(null);
  const move = (e: React.PointerEvent) => {
    const r = svg.current!.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * W;
    let best = pts[0];
    for (const p of pts) if (Math.abs(x(p.t) - px) < Math.abs(x(best.t) - px)) best = p;
    setHover(best ?? null);
  };
  const last = pts[pts.length - 1];
  return (
    <figure className="trend">
      <figcaption>
        <span>{title}</span>
        <b>{last ? `${last.v.toFixed(0)}${unit}` : "没有数据"}</b>
      </figcaption>
      {pts.length === 0 ? (
        <div className="trend-empty">这段时间没有采到数据</div>
      ) : (
        <div className="trend-plot">
          <svg ref={svg} viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`${title}趋势，最新 ${last.v.toFixed(0)}${unit}`}
            onPointerMove={move} onPointerLeave={() => setHover(null)}>
            {[0, 50, 100].map((g) => (
              <g key={g}>
                <line className="grid" x1={L} x2={W - R} y1={y(g)} y2={y(g)} />
                <text className="axis" x={L - 6} y={y(g) + 3.5} textAnchor="end">{g}</text>
              </g>
            ))}
            <text className="axis" x={L} y={H - 5}>{shortTime(t0)}</text>
            <text className="axis" x={W - R} y={H - 5} textAnchor="end">{shortTime(t1)}</text>
            <path className="trend-area" d={area} />
            <path className="trend-line" d={path} id={uid} />
            {hover && (
              <g>
                <line className="cross" x1={x(hover.t)} x2={x(hover.t)} y1={T} y2={H - B} />
                <circle className="dot" cx={x(hover.t)} cy={y(hover.v)} r="4" />
              </g>
            )}
          </svg>
          {hover && (
            <div className="tip" style={{ left: `${(x(hover.t) / W) * 100}%` }}>
              <b>{hover.v.toFixed(1)}{unit}</b> {new Date(hover.t).toLocaleString("zh-CN", { hour12: false, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}
            </div>
          )}
        </div>
      )}
    </figure>
  );
}

function median(a: number[]): number {
  const s = [...a].sort((x, y) => x - y);
  return s[Math.floor(s.length / 2)] ?? 0;
}

function shortTime(t: number): string {
  return new Date(t).toLocaleString("zh-CN", { hour12: false, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

// 界面里的线性图标，1.5 像素描边，和状态灯分开，不带颜色语义。
const glyphs: Record<string, ReactNode> = {
  search: <path d="M7.2 12.4a5.2 5.2 0 1 1 0-10.4 5.2 5.2 0 0 1 0 10.4ZM11 11l3.5 3.5" />,
  overview: <path d="M8 14.2a6.2 6.2 0 1 1 0-12.4 6.2 6.2 0 0 1 0 12.4ZM8 9.6a1.6 1.6 0 1 1 0-3.2 1.6 1.6 0 0 1 0 3.2ZM8 1.8v1.6M14.2 8h-1.6" />,
  devices: <path d="M2.5 3h11v6.5h-11zM6 13h4M8 9.5V13" />,
  services: <path d="M2.5 4.5h11M2.5 8h11M2.5 11.5h11M4.5 4.5h.01M4.5 8h.01M4.5 11.5h.01" />,
  projects: <path d="M2.5 3.5h4l1.5 1.5h5.5v7.5h-11z" />,
  repos: <path d="M4.5 2.5v11M4.5 13.5a1.6 1.6 0 1 0 0-.01M11.5 5.5a1.6 1.6 0 1 0 0-.01M11.5 7c0 2.5-2 3-7 4" />,
  access: <path d="M6 10a3.2 3.2 0 1 1 2.3-.98L13.5 14M11 11.5l1.4-1.4" />,
  sources: <path d="M8 13.5v-6M5.2 4.8a4 4 0 0 1 5.6 0M3.2 2.8a6.8 6.8 0 0 1 9.6 0M8 7.5h.01" />,
  more: <path d="M3.5 8h.01M8 8h.01M12.5 8h.01" />,
};

export function Glyph({ name }: { name: keyof typeof glyphs | string }) {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      {glyphs[name]}
    </svg>
  );
}

const icons: Record<string, ReactNode> = {
  server: <path d="M3 3.5h10v3.5H3zM3 9h10v3.5H3zM5.2 5.2h.01M5.2 10.8h.01" />,
  pc: <path d="M3.5 3.5h9v6.5h-9zM1.8 12.5h12.4" />,
  phone: <path d="M5 1.8h6v12.4H5zM7.4 12h1.2" />,
  tablet: <path d="M3.5 2h9v12h-9zM7.4 12h1.2" />,
  wearable: <path d="M5 4.8h6v6.4H5zM6 4.8 6.6 1.8h2.8L10 4.8M6 11.2l.6 3h2.8l.6-3" />,
  other: <path d="M3 3h10v10H3z" />,
};

export function DeviceIcon({ type }: { type: string }) {
  return (
    <svg className="dev-icon" viewBox="0 0 16 16" aria-hidden="true">
      {icons[type] ?? icons.other}
    </svg>
  );
}
