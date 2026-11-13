import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useApp } from "./app";
import { goFromLayer, kindLabel, useData } from "./lib";
import type { Card } from "./types";
import { Glyph, Lamp } from "./ui";

// 命令面板：Ctrl K 或 / 打开。搜对象、跳页面、切空间、切显示模式，全部能用键盘完成。

interface Item {
  key: string;
  group: string;
  label: string;
  meta?: string;
  icon?: ReactNode;
  run: () => void;
}

export const pages = [
  ["/", "总览"], ["/devices", "设备"], ["/services", "服务"], ["/projects", "项目"],
  ["/repos", "仓库"], ["/access", "访问"], ["/sources", "接入"],
] as const;

export function Palette({ onClose, onLogout }: { onClose: () => void; onLogout: () => void }) {
  const { workspaces, space, setSpace, theme, setTheme, spaceName } = useApp();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const term = q.trim();
  const { data } = useData<{ items: Card[] }>(term ? `/api/v1/entities?limit=8&q=${encodeURIComponent(term)}` : null);

  const items = useMemo(() => {
    const go = (h: string) => () => { goFromLayer(h); onClose(); };
    const match = (s: string) => !term || s.toLowerCase().includes(term.toLowerCase());
    const out: Item[] = [];
    for (const c of data?.items ?? []) {
      const ws = spaceName(c.workspace_id || c.workspace);
      out.push({ key: c.id, group: "对象", label: c.name, meta: ws ? `${kindLabel[c.kind]} · ${ws}` : kindLabel[c.kind], icon: <Lamp state={c.health.state} freshness={c.health.freshness} size={11} />, run: go(`#/e/${c.id}`) });
    }
    for (const [to, label] of pages) if (match(label)) out.push({ key: `p${to}`, group: "页面", label, icon: <span className="glyph">→</span>, run: go(`#${to}`) });
    const spaces = [{ slug: "", name: "全部空间" }, ...workspaces];
    for (const w of spaces) {
      if (w.slug === space || !match(`${w.name} 空间`)) continue;
      out.push({ key: `s${w.slug}`, group: "切换空间", label: w.name, icon: <span className="glyph">◇</span>, run: () => { setSpace(w.slug); onClose(); } });
    }
    const other = theme === "dark" ? "日间" : "夜间";
    if (match(`${other}模式 显示 主题`)) out.push({ key: "theme", group: "操作", label: `切换到${other}模式`, icon: <span className="glyph">◐</span>, run: () => { setTheme(theme === "dark" ? "light" : "dark"); onClose(); } });
    if (match("退出登录")) out.push({ key: "logout", group: "操作", label: "退出登录", icon: <span className="glyph">⏻</span>, run: () => { onClose(); onLogout(); } });
    return out;
  }, [data, term, workspaces, space, theme, setSpace, setTheme, spaceName, onClose, onLogout]);

  useEffect(() => setSel(0), [term, data]);
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    input.current?.focus();
    return () => prev?.focus?.();
  }, []);
  useEffect(() => {
    list.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  }, [sel]);

  const key = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setSel((n) => Math.min(items.length - 1, n + 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setSel((n) => Math.max(0, n - 1)); }
    else if (e.key === "Enter") { e.preventDefault(); items[sel]?.run(); }
    else if (e.key === "Escape") { e.preventDefault(); onClose(); }
  };

  let last = "";
  return (
    <div className="palette-scrim" onClick={onClose}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="搜索和跳转" onClick={(e) => e.stopPropagation()} onKeyDown={key}>
        <div className="palette-input">
          <Glyph name="search" />
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索设备、服务、项目，或输入页面名"
            role="combobox" aria-expanded="true" aria-controls="palette-list" aria-activedescendant={items[sel] ? `pi-${items[sel].key}` : undefined} />
        </div>
        <div className="palette-list" id="palette-list" role="listbox" ref={list}>
          {items.map((it, i) => {
            const head = it.group !== last ? <div className="palette-group" role="presentation">{it.group}</div> : null;
            last = it.group;
            return (
              <div key={it.key} role="presentation">
                {head}
                <button id={`pi-${it.key}`} role="option" aria-selected={i === sel} className="palette-item" tabIndex={-1}
                  onMouseMove={() => sel !== i && setSel(i)} onClick={it.run}>
                  {it.icon}
                  <span className="name">{it.label}</span>
                  {it.meta && <span className="meta">{it.meta}</span>}
                </button>
              </div>
            );
          })}
          {items.length === 0 && <p className="palette-empty">{term && !data ? "正在搜索…" : `没有找到“${term}”`}</p>}
        </div>
        <footer className="palette-foot">
          <span><kbd className="kbd">↑</kbd><kbd className="kbd">↓</kbd> 选择</span>
          <span><kbd className="kbd">Enter</kbd> 打开</span>
          <span><kbd className="kbd">Esc</kbd> 关闭</span>
        </footer>
      </div>
    </div>
  );
}
