import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "./lib";
import type { Card, Sample } from "./types";
import { Lamp } from "./ui";

// 观测带：每台接入采集的主机一条通道，横轴是最近 60 分钟。
// 每一根竖线都是一次真实上报，高度是当时的 CPU 占用；没收到数据的地方就是空白，
// 空白超过 3 个周期在基线上标出来。这里不画任何没有发生过的东西。

const WINDOW = 60 * 60 * 1000;
const H = 34;

interface Series {
  from: number;
  to: number;
  items: Sample[];
}

const rank: Record<string, number> = { down: 0, degraded: 1, unknown: 2, ok: 3 };

function useSeries(ids: string[]) {
  const key = ids.join(",");
  const [data, setData] = useState<Record<string, Series>>({});
  useEffect(() => {
    if (!key) return;
    let live = true;
    const load = () =>
      Promise.all(
        key.split(",").map((id) =>
          api.get<{ from: string; to: string; items: Sample[] }>(`/api/v1/entities/${id}/metrics?range=1h`).then(
            (r) => [id, { from: new Date(r.from).getTime(), to: new Date(r.to).getTime(), items: r.items }] as const,
            () => null,
          ),
        ),
      ).then((rows) => {
        if (!live) return;
        setData((old) => {
          const next = { ...old };
          for (const r of rows) if (r) next[r[0]] = r[1];
          return next;
        });
      });
    load();
    const t = setInterval(() => document.visibilityState === "visible" && load(), 30000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [key]);
  return data;
}

function useWidth() {
  const ref = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(0);
  useEffect(() => {
    const el = ref.current!;
    const ro = new ResizeObserver(() => setW(el.clientWidth));
    ro.observe(el);
    setW(el.clientWidth);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

function median(a: number[]) {
  const s = [...a].sort((x, y) => x - y);
  return s[Math.floor(s.length / 2)] ?? 0;
}

const pct = (v: number | null | undefined) => (v == null ? "—" : `${v < 10 ? v.toFixed(1) : Math.round(v)}%`);
const level = (v: number | null | undefined, warn: number, fail: number) => (v == null ? "" : v >= fail ? "hot" : v >= warn ? "warm" : "");
const clock = (t: number) => new Date(t).toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit" });

function Channel({ c, s }: { c: Card; s: Series | undefined }) {
  const d = c.device!;
  const [ref, w] = useWidth();
  const [hover, setHover] = useState<Sample | null>(null);
  const seen = useRef<number>(0);

  const to = s?.to ?? Date.now();
  const from = to - WINDOW;
  const pts = useMemo(
    () => (s?.items ?? []).map((p) => ({ t: new Date(p.ts).getTime(), p })).filter((x) => x.t >= from),
    [s, from],
  );
  const step = pts.length > 2 ? Math.max(15000, median(pts.slice(1).map((x, i) => x.t - pts[i].t))) : 60000;
  const expected = Math.round(WINDOW / step);
  const x = (t: number) => ((t - from) / WINDOW) * w;

  // 断档：相邻两次上报之间超过 3 个周期，或者窗口开头一段没有数据。
  const gaps: [number, number][] = [];
  if (s) {
    const edges = [from - step, ...pts.map((p) => p.t), to];
    for (let i = 1; i < edges.length; i++) if (edges[i] - edges[i - 1] > step * 3) gaps.push([Math.max(from, edges[i - 1] + step), edges[i]]);
  }
  const prevSeen = seen.current;
  useEffect(() => {
    if (pts.length) seen.current = pts[pts.length - 1].t;
  }, [pts]);

  const tickW = w > 0 ? Math.max(1.5, Math.min(3, (w / expected) * 0.34)) : 2;
  const move = (e: React.PointerEvent) => {
    if (!pts.length) return;
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const px = e.clientX - r.left;
    let best = pts[0];
    for (const p of pts) if (Math.abs(x(p.t) - px) < Math.abs(x(best.t) - px)) best = p;
    setHover(best.p);
  };

  return (
    <a className="chan" href={`#/e/${c.id}`} aria-label={`${c.name}：最近一小时上报 ${pts.length} 次，${c.health.reason}`}>
      <span className="chan-name">
        <Lamp state={c.health.state} freshness={c.health.freshness} size={12} />
        <strong>{c.name}</strong>
      </span>
      <div className="chan-track" ref={ref} onPointerMove={move} onPointerLeave={() => setHover(null)}>
        {w > 0 && (
          <svg aria-hidden="true">
            <line className="chan-base" x1="0" x2={w} y1={H - 0.5} y2={H - 0.5} />
            {gaps.map(([a, b], i) => (
              <rect key={i} className="chan-gap" x={x(a)} y={H - 2} width={Math.max(1, x(b) - x(a))} height="2" />
            ))}
            {pts.map(({ t, p }) => {
              const v = p.cpu_pct ?? 0;
              const h = 3 + (Math.min(100, Math.max(0, v)) / 100) * (H - 5);
              const recent = to - t < step * 3;
              return (
                <rect key={t} className={`chan-tick${recent ? " recent" : ""}${v >= 90 ? " hot" : v >= 70 ? " warm" : ""}${prevSeen && t > prevSeen ? " fresh" : ""}`}
                  x={x(t) - tickW / 2} y={H - 1 - h} width={tickW} height={h} />
              );
            })}
            <line className="chan-now" x1={w - 0.5} x2={w - 0.5} y1="0" y2={H} />
          </svg>
        )}
        {hover && (
          <span className="chan-tip" style={{ left: Math.min(Math.max(x(new Date(hover.ts).getTime()), 70), w - 70) }}>
            {clock(new Date(hover.ts).getTime())} · CPU {pct(hover.cpu_pct)} · 内存 {pct(hover.mem_pct)}
          </span>
        )}
      </div>
      <span className="chan-read">
        <span><em>CPU</em><b className={level(d.cpu_pct, 90, 101)}>{pct(d.cpu_pct)}</b></span>
        <span><em>内存</em><b className={level(d.mem_pct, 90, 97)}>{pct(d.mem_pct)}</b></span>
        <span><em>磁盘</em><b className={level(d.disk_pct, 85, 92)}>{pct(d.disk_pct)}</b></span>
        <span className="count" title={gaps.length ? "最近一小时收到的上报次数，其间有断档" : "最近一小时收到的上报次数"}>
          <em>上报</em><b className={gaps.length ? "warm" : ""}>{s ? `${pts.length} 次` : "…"}</b>
        </span>
      </span>
    </a>
  );
}

export function Telemetry({ devices }: { devices: Card[] }) {
  const live = useMemo(
    () => devices.filter((c) => c.device?.last_seen).sort((a, b) => (rank[a.health.state] ?? 4) - (rank[b.health.state] ?? 4) || a.name.localeCompare(b.name, "zh")),
    [devices],
  );
  const series = useSeries(live.map((c) => c.id));
  const quiet = devices.length - live.length;
  if (live.length === 0) return null;
  return (
    <section className="scope" aria-label="观测带">
      <header className="scope-head">
        <h2>观测带</h2>
        <p>最近 60 分钟。每根竖线是一次真实上报，高度是当时的 CPU；空白就是没收到数据。</p>
      </header>
      {live.map((c) => <Channel key={c.id} c={c} s={series[c.id]} />)}
      <div className="chan chan-axis" aria-hidden="true">
        <span />
        <span className="axis-row"><span>60 分钟前</span><span>30 分钟前</span><span>现在</span></span>
        <span />
      </div>
      {quiet > 0 && <p className="scope-foot">另有 {quiet} 台设备还没有接入采集，不在这里显示。</p>}
    </section>
  );
}
