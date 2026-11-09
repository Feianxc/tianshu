import { useEffect, useMemo, useRef, useState } from "react";
import type { Card, HealthState, Lamp as LampT, WallRow } from "./types";
import { healthLabel } from "./lib";

// 星图：天枢在中心，每个空间一圈轨道，轨道上的星是这个空间的设备（大）和服务（小）。
// 星的形状和颜色就是状态灯的编码。设备的新一次上报到达时，沿连线向中心送回一个脉冲；
// 每次对账完成，中心泛起一圈波纹。所有动静都对应一次真实发生的数据变化。

const S = 460, C = S / 2, R0 = 74, R1 = 186, BEZEL = 204;

interface Node {
  l: LampT;
  device: boolean;
  x: number;
  y: number;
  ring: number;
  space: string;
}

function glyph(state: HealthState, stale: boolean, x: number, y: number, k: number) {
  const s = (v: number) => v * k;
  if (stale || state === "unknown") return <circle cx={x} cy={y} r={s(3.6)} className="sm-ring" />;
  if (state === "ok") return <circle cx={x} cy={y} r={s(3.8)} className="sm-ok" />;
  if (state === "degraded") return <path d={`M${x} ${y - s(4.6)}L${x + s(4.6)} ${y + s(3.4)}H${x - s(4.6)}Z`} className="sm-warn" />;
  if (state === "down") return <path d={`M${x} ${y - s(5)}L${x + s(5)} ${y}L${x} ${y + s(5)}L${x - s(5)} ${y}Z`} className="sm-fail" />;
  if (state === "paused") return <circle cx={x} cy={y} r={s(3.6)} className="sm-dash" />;
  return <circle cx={x} cy={y} r={s(1.8)} className="sm-off" />;
}

// 有对象的空间才占一圈；对象少的放内圈，多的放外圈，外圈周长长，星不会挤在一起。
function orbits(rows: WallRow[]) {
  const size = (r: WallRow) => r.devices.length + r.services.length;
  return rows.filter((r) => size(r) > 0).sort((a, b) => size(a) - size(b));
}

interface Pulse {
  key: string;
  x: number;
  y: number;
}

export function StarMap({ rows, devices, generatedAt }: { rows: WallRow[]; devices: Card[]; generatedAt: string }) {
  const live = useMemo(() => orbits(rows), [rows]);
  const nodes = useMemo(() => {
    const out: Node[] = [];
    live.forEach((r, i) => {
      const radius = live.length === 1 ? (R0 + R1) / 2 : R0 + ((R1 - R0) * i) / (live.length - 1);
      const list = [...r.devices.map((l) => ({ l, device: true })), ...r.services.map((l) => ({ l, device: false }))];
      // 每圈的起点错开黄金角，免得所有轨道的第一颗星排成一条线。
      const start = -Math.PI / 2 + i * 2.39996;
      list.forEach((n, k) => {
        const a = start + (k * 2 * Math.PI) / list.length;
        out.push({ ...n, x: C + Math.cos(a) * radius, y: C + Math.sin(a) * radius, ring: radius, space: r.workspace.name });
      });
    });
    return out;
  }, [live]);

  // 记住每台设备上一次的上报时间，变了就送一个脉冲回中心。第一次加载不送。
  const seen = useRef<Map<string, string>>(new Map());
  const first = useRef(true);
  const [pulses, setPulses] = useState<Pulse[]>([]);
  const timers = useRef<number[]>([]);
  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  useEffect(() => {
    const fresh: Pulse[] = [];
    for (const c of devices) {
      const t = c.device?.last_seen;
      if (!t) continue;
      const before = seen.current.get(c.id);
      seen.current.set(c.id, t);
      if (first.current || before === t) continue;
      const n = nodes.find((x) => x.l.id === c.id);
      if (n) fresh.push({ key: `${c.id}-${t}`, x: n.x, y: n.y });
    }
    first.current = false;
    if (!fresh.length) return;
    setPulses((p) => [...p, ...fresh]);
    timers.current.push(window.setTimeout(() => setPulses((p) => p.filter((x) => !fresh.includes(x))), 1600));
  }, [devices, nodes]);

  const [hover, setHover] = useState<Node | null>(null);
  const bad = nodes.filter((n) => n.l.state === "down" || n.l.state === "degraded").length;

  return (
    <figure className="starmap">
      <svg viewBox={`0 0 ${S} ${S}`} role="group" aria-label={`星图：${live.length} 个空间、${nodes.length} 个对象，${bad} 个异常`}>
        <defs>
          <radialGradient id="sm-halo">
            <stop offset="0" stopColor="var(--brand)" stopOpacity=".55" />
            <stop offset="1" stopColor="var(--brand)" stopOpacity="0" />
          </radialGradient>
        </defs>
        <circle className="sm-bezel" cx={C} cy={C} r={BEZEL} />
        {Array.from({ length: 120 }, (_, i) => {
          const a = (i * 3 * Math.PI) / 180, long = i % 10 === 0, r2 = BEZEL + (long ? 9 : 4);
          return <line key={i} className={long ? "sm-tick sm-tick-l" : "sm-tick"} x1={C + Math.cos(a) * BEZEL} y1={C + Math.sin(a) * BEZEL} x2={C + Math.cos(a) * r2} y2={C + Math.sin(a) * r2} />;
        })}
        {live.map((r, i) => {
          const radius = live.length === 1 ? (R0 + R1) / 2 : R0 + ((R1 - R0) * i) / (live.length - 1);
          return <circle key={r.workspace.id} className={r.workspace.confirmed ? "sm-orbit" : "sm-orbit sm-orbit-tbd"} cx={C} cy={C} r={radius} style={{ stroke: r.workspace.color }} />;
        })}
        {nodes.filter((n) => n.device).map((n) => <line key={`s-${n.l.id}`} className="sm-spoke" x1={C} y1={C} x2={n.x} y2={n.y} />)}
        <circle cx={C} cy={C} r="30" fill="url(#sm-halo)" />
        <circle className="sm-hub-ring" cx={C} cy={C} r="15" />
        <circle className="sm-hub" cx={C} cy={C} r="5.5" />
        <circle key={generatedAt} className="sm-ripple" cx={C} cy={C} r="40" />
        {pulses.map((p) => (
          <circle key={p.key} className="sm-pulse" cx={p.x} cy={p.y} r="2.6" style={{ "--dx": `${C - p.x}px`, "--dy": `${C - p.y}px` } as React.CSSProperties} />
        ))}
        {nodes.map((n) => (
          <a key={n.l.id} href={`#/e/${n.l.id}`} className="sm-node" tabIndex={-1} aria-label={`${n.l.name}，${n.space}，${n.l.reason}`}
            onMouseEnter={() => setHover(n)} onMouseLeave={() => setHover(null)}>
            <circle cx={n.x} cy={n.y} r="18" className="sm-hit" />
            {glyph(n.l.state, n.l.freshness === "stale" && (n.l.state === "ok" || n.l.state === "unknown"), n.x, n.y, n.device ? 1.25 : 0.82)}
          </a>
        ))}
        {hover && (() => {
          const right = hover.x < C;
          const w = Math.max(hover.l.name.length, hover.space.length + 4) * 12 + 20;
          const x = Math.min(Math.max(right ? hover.x + 14 : hover.x - 14 - w, 4), S - w - 4);
          const y = Math.min(Math.max(hover.y - 24, 6), S - 54);
          return (
            <g className="sm-label" pointerEvents="none">
              <rect x={x} y={y} width={w} height="46" rx="5" />
              <text x={x + 10} y={y + 19}>{hover.l.name}</text>
              <text x={x + 10} y={y + 36} className="sm-label-sub">{healthLabel[hover.l.state] ?? hover.l.state} · {hover.space}</text>
            </g>
          );
        })()}
      </svg>
    </figure>
  );
}

// 星图的图例放在首屏左栏：一圈轨道一个空间，从内到外。
export function StarLegend({ rows }: { rows: WallRow[] }) {
  const live = orbits(rows);
  return (
    <div className="sm-side">
      <ul className="sm-legend">
        {live.map((r, i) => {
          const issues = [...r.devices, ...r.services].filter((l) => l.state === "down" || l.state === "degraded").length;
          return (
            <li key={r.workspace.id}>
              <span className="sm-orbit-no mono">{i === 0 ? "内" : i === live.length - 1 ? "外" : "·"}</span>
              <i style={{ background: r.workspace.color }} aria-hidden="true" />
              <span className="sm-name">{r.workspace.name}</span>
              <b className="mono">{r.devices.length}<em>台</em>{r.services.length}<em>服务</em></b>
              <b className={`mono sm-count ${issues ? "sm-issues" : ""}`}>{issues || "—"}<em>{issues ? "异常" : ""}</em></b>
            </li>
          );
        })}
      </ul>
      <p className="sm-key">星图里一圈轨道一个空间，大星是设备，小星是服务。设备收到新上报时，会沿连线向中心送回一个光点。</p>
    </div>
  );
}
