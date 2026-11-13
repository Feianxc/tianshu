import { useCallback, useEffect, useRef, useState } from "react";

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: { "X-Wanglou": "1", ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let code = "http", message = `请求失败（${res.status}）`;
    try {
      const j = await res.json();
      code = j.error?.code ?? code;
      message = j.error?.message ?? message;
    } catch {
      /* 非 JSON 错误保留默认文案 */
    }
    throw new ApiError(res.status, code, message);
  }
  return res.json() as Promise<T>;
}

export const api = {
  get: <T,>(p: string) => call<T>("GET", p),
  post: <T,>(p: string, b: unknown) => call<T>("POST", p, b),
  patch: <T,>(p: string, b: unknown) => call<T>("PATCH", p, b),
  del: <T,>(p: string) => call<T>("DELETE", p),
};

export interface Loaded<T> {
  data: T | undefined;
  error: ApiError | Error | undefined;
  loading: boolean;
  reload: () => void;
}

// 拉取数据并定时刷新。页面隐藏时不刷新；刷新失败时保留旧数据并给出错误，不清空界面。
export function useData<T>(path: string | null, refreshMs = 0): Loaded<T> {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<Error>();
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);
  const load = useCallback(() => {
    // A cleared path still bumps the sequence, so an answer to the previous path is ignored.
    const id = ++seq.current;
    if (!path) return;
    api.get<T>(path).then(
      (d) => {
        if (id === seq.current) {
          setData(d);
          setError(undefined);
          setLoading(false);
        }
      },
      (e) => {
        if (id === seq.current) {
          setError(e);
          setLoading(false);
        }
      },
    );
  }, [path]);
  useEffect(() => {
    setLoading(true);
    setData(undefined);
    load();
    if (!refreshMs) return;
    const t = setInterval(() => {
      if (document.visibilityState === "visible") load();
    }, refreshMs);
    return () => clearInterval(t);
  }, [load, refreshMs]);
  return { data, error, loading, reload: load };
}

export function useHash(): string {
  const [h, setH] = useState(() => location.hash.slice(1) || "/");
  useEffect(() => {
    const f = () => {
      setH(location.hash.slice(1) || "/");
      window.scrollTo(0, 0);
    };
    addEventListener("hashchange", f);
    return () => removeEventListener("hashchange", f);
  }, []);
  return h;
}

// 弹层打开时占一条历史记录：手机返回键、浏览器后退先关弹层，而不是离开当前页。
// 用界面关掉时把这条记录退掉；因为点了链接而关掉时，新页面已经有自己的记录，不再后退。
export function useBackToClose(open: boolean, close: () => void) {
  const latest = useRef(close);
  latest.current = close;
  useEffect(() => {
    if (!open) return;
    history.pushState({ wlLayer: true }, "");
    let popped = false;
    const onPop = () => {
      popped = true;
      latest.current();
    };
    addEventListener("popstate", onPop);
    return () => {
      removeEventListener("popstate", onPop);
      if (!popped && history.state?.wlLayer) history.back();
    };
  }, [open]);
}

// 从弹层里跳页面：替换掉弹层那条记录，免得返回时多按一次。
export function goFromLayer(hash: string) {
  if (history.state?.wlLayer) location.replace(hash);
  else location.hash = hash;
}

// 所有“几分钟前”共用一个节拍。
const listeners = new Set<() => void>();
setInterval(() => listeners.forEach((f) => f()), 15000);
export function useTick() {
  const [, set] = useState(0);
  useEffect(() => {
    const f = () => set((n) => n + 1);
    listeners.add(f);
    return () => {
      listeners.delete(f);
    };
  }, []);
}

export function ago(iso: string | null | undefined): string {
  if (!iso) return "从未";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
  if (s < 172800) return `${Math.floor(s / 3600)} 小时前`;
  return `${Math.floor(s / 86400)} 天前`;
}

export function fullTime(iso: string | null | undefined): string {
  if (!iso) return "";
  return new Date(iso).toLocaleString("zh-CN", { hour12: false });
}

export function uptime(s: number | null | undefined): string {
  if (s == null) return "未知";
  if (s < 3600) return `${Math.floor(s / 60)} 分钟`;
  if (s < 172800) return `${Math.floor(s / 3600)} 小时`;
  return `${Math.floor(s / 86400)} 天`;
}

export const healthLabel: Record<string, string> = {
  ok: "正常", degraded: "降级", down: "故障", unknown: "未知", paused: "已暂停",
  offline: "未上线", retired: "已退役", planned: "未上线",
};

export const freshLabel: Record<string, string> = {
  fresh: "数据新鲜", partial: "部分过期", stale: "数据过期", missing: "没有数据",
};

export const stageLabel: Record<string, string> = {
  idea: "想法", planned: "规划", building: "开发", verifying: "验证",
  delivered: "交付", maintaining: "维护", paused: "暂停", archived: "归档",
};

export const stageOrder = ["idea", "planned", "building", "verifying", "delivered", "maintaining"];

export const kindLabel: Record<string, string> = {
  device: "设备", service: "服务", project: "项目", repository: "仓库", credential: "凭据",
};

export const deviceTypeLabel: Record<string, string> = {
  server: "服务器", pc: "电脑", phone: "手机", tablet: "平板", wearable: "穿戴", router: "路由器", iot: "物联网", other: "其他",
};

export const relationLabel: Record<string, [string, string]> = {
  runs_on: ["运行于", "运行着"],
  part_of: ["属于", "包含"],
  built_from: ["构建自", "构建出"],
  depends_on: ["依赖", "被依赖"],
  uses: ["使用", "被使用于"],
};

export const collectorLabel: Record<string, string> = {
  agent: "采集器", ssh: "SSH 只读采集", phone: "手机上报", termux: "Termux 上报", shortcut: "快捷指令上报",
};

export const storageLabel: Record<string, string> = {
  plaintext_file: "明文文件", local_file: "本机文件", os_keyring: "系统钥匙串", local_config: "本机配置", mixia: "密匣",
};

export const sourceCondition: Record<string, string> = {
  ok: "正常", stale: "已停止", error: "出错", never: "从未成功",
};

export function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export function pct(v: number | null | undefined): string {
  return v == null ? "未测" : `${Math.round(v)}%`;
}

const THEME_KEY = "wl-theme";
export type Theme = "light" | "dark";

// 默认夜间，不跟随系统：这是天枢的样子，不是一个可有可无的暗色模式。日间给白天和户外用。
export function loadTheme(): Theme {
  try {
    if (localStorage.getItem(THEME_KEY) === "light") return "light";
  } catch {
    /* 隐私模式下不可用 */
  }
  return "dark";
}

export function applyTheme(t: Theme) {
  const root = document.documentElement;
  root.setAttribute("data-theme", t);
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch {
    /* 同上 */
  }
}
