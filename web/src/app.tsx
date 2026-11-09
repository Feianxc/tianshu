import { createContext, useCallback, useContext, useEffect, useState, type FormEvent } from "react";
import { ApiError, api, applyTheme, goFromLayer, loadTheme, useBackToClose, useData, useHash, type Loaded, type Theme } from "./lib";
import type { Me, Overview, Workspace } from "./types";
import { Glyph } from "./ui";
import { BRAND, Mark, SkyChart, Wordmark } from "./brand";
import { Palette, pages } from "./palette";
import { PairAlerts, PairLogin } from "./pairing";
import { OverviewPage } from "./pages/Overview";
import { DevicesPage, ServicesPage, ReposPage } from "./pages/Lists";
import { ProjectsPage } from "./pages/Projects";
import { AccessPage } from "./pages/Access";
import { SourcesPage } from "./pages/Sources";
import { DetailPage } from "./pages/Detail";
import { ConsentPage } from "./pages/Consent";

export interface Ctx {
  me: Me;
  workspaces: Workspace[];
  space: string; // 工作空间 slug，空串为全部
  setSpace: (s: string) => void;
  spaceName: (id: string) => string;
  can: (scope: string) => boolean;
  ov: Loaded<Overview>;
  theme: Theme;
  setTheme: (t: Theme) => void;
}

const AppCtx = createContext<Ctx>(null!);
export const useApp = () => useContext(AppCtx);

export const hms = (iso: string | null | undefined) =>
  iso ? new Date(iso).toLocaleTimeString("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "--:--:--";

function Login({ onDone }: { onDone: () => void }) {
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      await api.post("/api/v1/session", { token });
      onDone();
    } catch (x) {
      setErr((x as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="login">
      <div className="login-art"><SkyChart /></div>
      <div className="login-box">
        <div className="login-brand"><Mark /><h1><Wordmark /></h1></div>
        <form className="login-token" onSubmit={submit}>
          <p>输入管理员令牌。令牌在服务器上用 wanglou init 生成，只显示一次。</p>
          <label>
            <span>管理员令牌</span>
            <input type="password" autoComplete="current-password" value={token} onChange={(e) => setToken(e.target.value)} required />
          </label>
          {err && <p className="form-error" role="alert">{err}</p>}
          <button className="primary" disabled={busy || !token}>{busy ? "正在登录" : "登录"}</button>
        </form>
        <p className="login-or"><span>或者</span></p>
        <PairLogin onDone={onDone} />
        <p className="login-foot"><span>个人总控台</span><span className="mono">{location.host}</span></p>
      </div>
    </main>
  );
}

export function App() {
  const [session, setSession] = useState(0);
  const me = useData<Me>(`/api/v1/me?s=${session}`);
  const unauth = me.error instanceof ApiError && me.error.status === 401;
  if (unauth) return <Login onDone={() => setSession((n) => n + 1)} />;
  if (!me.data) return <div className="boot"><Mark /></div>;
  return <Shell me={me.data} onLogout={() => setSession((n) => n + 1)} />;
}

const navGlyph: Record<string, string> = {
  "/": "overview", "/devices": "devices", "/services": "services", "/projects": "projects",
  "/repos": "repos", "/access": "access", "/sources": "sources",
};

function attentionTone(o: Overview | undefined) {
  if (!o || o.attention.length === 0) return "";
  return o.attention.some((f) => f.severity === "critical") ? "hot" : "warm";
}

function Shell({ me, onLogout }: { me: Me; onLogout: () => void }) {
  const route = useHash();
  const ws = useData<{ items: Workspace[] }>("/api/v1/workspaces");
  const [space, setSpaceState] = useState(() => {
    try {
      return localStorage.getItem("wl-space") ?? "";
    } catch {
      return "";
    }
  });
  const setSpace = useCallback((s: string) => {
    setSpaceState(s);
    try {
      localStorage.setItem("wl-space", s);
    } catch {
      /* 不可用时只在本次生效 */
    }
  }, []);
  const [theme, setThemeState] = useState<Theme>(loadTheme);
  const setTheme = useCallback((t: Theme) => {
    setThemeState(t);
    applyTheme(t);
  }, []);
  const ov = useData<Overview>(`/api/v1/overview${space ? `?workspace=${space}` : ""}`, 15000);
  const [palette, setPalette] = useState(false);
  const [more, setMore] = useState(false);
  const [scrolled, setScrolled] = useState(false);

  useEffect(() => {
    const f = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if (((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") || (e.key === "/" && !typing)) {
        e.preventDefault();
        setPalette(true);
      }
    };
    const s = () => setScrolled(scrollY > 4);
    addEventListener("keydown", f);
    addEventListener("scroll", s, { passive: true });
    return () => {
      removeEventListener("keydown", f);
      removeEventListener("scroll", s);
    };
  }, []);
  useEffect(() => setMore(false), [route]);
  useBackToClose(more, () => setMore(false));
  const layerLink = (e: React.MouseEvent<HTMLAnchorElement>) => {
    e.preventDefault();
    goFromLayer(e.currentTarget.getAttribute("href")!);
  };
  useBackToClose(palette, () => setPalette(false));

  const workspaces = ws.data?.items ?? [];
  const spaceName = useCallback((id: string) => workspaces.find((w) => w.id === id || w.slug === id)?.name ?? "", [workspaces]);
  const ctx: Ctx = { me, workspaces, space, setSpace, spaceName, can: (s) => me.scopes.includes(s), ov, theme, setTheme };
  const logout = async () => {
    await api.del("/api/v1/session");
    onLogout();
  };
  const section = "/" + route.split("/")[1];
  const at = (to: string) => (section === to || (to === "/" && route === "/") ? "page" : undefined);
  let page;
  if (route.startsWith("/e/")) page = <DetailPage key={route} id={route.slice(3)} />;
  else if (route.startsWith("/oauth/")) page = <ConsentPage id={route.slice(7)} />;
  else if (section === "/devices") page = <DevicesPage />;
  else if (section === "/services") page = <ServicesPage />;
  else if (section === "/projects") page = <ProjectsPage />;
  else if (section === "/repos") page = <ReposPage />;
  else if (section === "/access") page = <AccessPage />;
  else if (section === "/sources") page = <SourcesPage />;
  else page = <OverviewPage />;

  const o = ov.data;
  const tone = attentionTone(o);
  const counts: Record<string, number | undefined> = {
    "/": o?.attention.length, "/devices": o?.totals.device, "/services": o?.totals.service,
    "/projects": o?.totals.project, "/repos": o?.totals.repository,
  };
  const current = space ? spaceName(space) || "空间" : "全部空间";

  return (
    <AppCtx.Provider value={ctx}>
      <div className="shell">
        <aside className="rail">
          <a className="brand" href="#/" aria-label={`${BRAND.zh}总览`}><Mark /><Wordmark /></a>
          <nav className="rail-nav" aria-label="主导航">
            {pages.map(([to, label]) => (
              <a key={to} href={`#${to}`} aria-current={at(to)}>
                <Glyph name={navGlyph[to]} />
                {label}
                {counts[to] != null && counts[to]! > 0 && <span className={`count mono ${to === "/" ? tone : ""}`}>{counts[to]}</span>}
              </a>
            ))}
          </nav>
          <div>
            <h2 className="rail-h">空间</h2>
            <div className="rail-spaces">
              <button className={space === "" ? "on" : ""} onClick={() => setSpace("")}><i className="swatch swatch-all" aria-hidden="true" /><span>全部空间</span></button>
              {workspaces.map((w) => (
                <button key={w.id} className={space === w.slug ? "on" : ""} onClick={() => setSpace(w.slug)} title={w.name}>
                  <i className="swatch" style={{ background: w.color }} aria-hidden="true" />
                  <span>{w.name}</span>
                  {!w.confirmed && <span className="mono">?</span>}
                </button>
              ))}
            </div>
          </div>
          <div className="rail-foot">
            <p className="clock"><span>最近对账</span><span className="mono">{hms(o?.generated_at)}</span></p>
            <div className="rail-foot-row">
              <button className="ghost" onClick={() => setTheme(theme === "dark" ? "light" : "dark")} title="切换显示模式">{theme === "dark" ? "日间模式" : "夜间模式"}</button>
              <button className="ghost" onClick={logout}>退出</button>
            </div>
          </div>
        </aside>
        <div className="main">
          <div className={scrolled ? "top scrolled" : "top"}>
            <a className="brand brand-m" href="#/" aria-label={`${BRAND.zh}总览`}><Mark /><Wordmark sub={false} /></a>
            <button className="cmd" onClick={() => setPalette(true)}>
              <Glyph name="search" />
              <span>搜索或跳转</span>
              <kbd className="kbd">Ctrl K</kbd>
            </button>
            <span className="top-space">{current}</span>
            <select className="space-m" aria-label="切换空间" value={space} onChange={(e) => setSpace(e.target.value)}>
              <option value="">全部空间</option>
              {workspaces.map((w) => <option key={w.id} value={w.slug}>{w.name}</option>)}
            </select>
            <button className="icon-btn" onClick={() => setPalette(true)} aria-label="搜索或跳转"><Glyph name="search" /></button>
          </div>
          <main className="content" key={section}>{page}</main>
        </div>
        <nav className="tabbar" aria-label="主导航">
          {([["/", "总览", "overview"], ["/devices", "设备", "devices"], ["/services", "服务", "services"], ["/projects", "项目", "projects"]] as const).map(([to, label, g]) => (
            <a key={to} href={`#${to}`} aria-current={at(to)}>
              <Glyph name={g} />
              {label}
              {to === "/" && tone && <span className={`badge ${tone === "warm" ? "warm" : ""}`}>{o!.attention.length}</span>}
            </a>
          ))}
          <button onClick={() => setMore(true)} aria-current={["/repos", "/access", "/sources"].includes(section) ? "page" : undefined} aria-haspopup="dialog">
            <Glyph name="more" />
            更多
          </button>
        </nav>
      </div>
      {me.kind === "human" && me.all_workspaces && me.scopes.includes("admin") && <PairAlerts />}
      {palette && <Palette onClose={() => setPalette(false)} onLogout={logout} />}
      {more && (
        <div className="sheet-scrim" onClick={() => setMore(false)}>
          <div className="sheet" role="dialog" aria-modal="true" aria-label="更多" onClick={(e) => e.stopPropagation()}>
            <div className="sheet-grip" />
            <a href="#/repos" onClick={layerLink} aria-current={at("/repos")}>仓库<span className="mono">{o?.totals.repository ?? ""}</span></a>
            <a href="#/access" onClick={layerLink} aria-current={at("/access")}>访问</a>
            <a href="#/sources" onClick={layerLink} aria-current={at("/sources")}>接入</a>
            <h3>空间</h3>
            {[{ slug: "", name: "全部空间", id: "" }, ...workspaces].map((w) => (
              <button key={w.slug || "all"} className={`sheet-btn ${space === w.slug ? "on" : ""}`} onClick={() => { setSpace(w.slug); setMore(false); }}>{w.name}</button>
            ))}
            <h3>显示</h3>
            <button className="sheet-btn" onClick={() => setTheme(theme === "dark" ? "light" : "dark")}>{theme === "dark" ? "切换到日间模式" : "切换到夜间模式"}</button>
            <button className="sheet-btn" onClick={logout}>退出登录</button>
          </div>
        </div>
      )}
    </AppCtx.Provider>
  );
}
