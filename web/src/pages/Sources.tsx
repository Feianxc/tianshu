import { useApp } from "../app";
import { api, sourceCondition, str, useData } from "../lib";
import type { AuditEvent, Discovery, SourceRow } from "../types";
import { Ago, Empty, Failure, Lamp, Panel } from "../ui";

const kindName: Record<string, string> = { agent: "采集器", ssh: "SSH 只读", connector: "连接器", probe: "探测", phone: "手机", termux: "Termux", shortcut: "快捷指令" };

function Inbox() {
  const { data, reload } = useData<{ items: Discovery[] }>("/api/v1/discoveries");
  const decide = async (d: Discovery, state: string) => {
    await api.post(`/api/v1/discoveries/${d.id}`, { state });
    reload();
  };
  const adopt = async (d: Discovery) => {
    const e = await api.post<{ id: string }>(`/api/v1/discoveries/${d.id}/adopt`, {});
    location.hash = `#/e/${e.id}`;
  };
  const items = data?.items ?? [];
  const units = items.filter((d) => d.kind === "unit");
  const repos = items.filter((d) => d.kind === "repo");
  return (
    <Panel title="待归属" note="采集器看到了，但目录里还没有。不会自动收录">
      {data && items.length === 0 && <Empty title="没有待归属的对象" />}
      {units.length > 0 && <h3 className="sub">运行中的系统服务</h3>}
      <ul className="inbox">
        {units.map((d) => (
          <li key={d.id}>
            <code>{d.key}</code>
            <span className="inbox-act">
              <button className="link" onClick={() => adopt(d)}>收录</button>
              <button className="link" onClick={() => decide(d, "ignored")}>忽略</button>
            </span>
          </li>
        ))}
      </ul>
      {repos.length > 0 && <h3 className="sub">本地 Git 仓库</h3>}
      <ul className="inbox">
        {repos.map((d) => (
          <li key={d.id}>
            <code>{d.key}</code>
            <small className={str(d.payload.remote) ? "" : "inbox-warn"}>{str(d.payload.remote) || "没有远端，只存在于这台电脑"}</small>
            <span className="inbox-act">
              {str(d.payload.remote) && <button className="link" onClick={() => adopt(d)}>收录</button>}
              <button className="link" onClick={() => decide(d, "ignored")}>忽略</button>
            </span>
          </li>
        ))}
      </ul>
    </Panel>
  );
}

export function SourcesPage() {
  const { can } = useApp();
  const src = useData<{ items: SourceRow[] }>(can("admin") ? "/api/v1/sources" : null, 15000);
  const audit = useData<{ items: AuditEvent[] }>(can("admin") ? "/api/v1/audit?limit=60" : null, 30000);
  if (!can("admin")) return <Empty title="需要管理员权限" />;
  if (src.error && !src.data) return <Failure error={src.error} retry={src.reload} />;
  return (
    <>
      <header className="page-head">
        <div>
          <h1>接入</h1>
          <p>每条数据从哪来、最后一次成功是什么时候。来源坏了，它提供的状态会过期，而不是继续显示正常。</p>
        </div>
      </header>
      <Panel title="数据来源">
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>来源</th><th>类型</th><th>状态</th><th>最后成功</th><th>周期</th><th>错误</th></tr></thead>
            <tbody>
              {(src.data?.items ?? []).map((s) => (
                <tr key={s.name}>
                  <td data-l="来源">{s.name}</td>
                  <td data-l="类型">{kindName[s.kind] ?? s.kind}</td>
                  <td data-l="状态"><span className="ci"><Lamp state={s.condition === "ok" ? "ok" : s.condition === "error" ? "down" : "unknown"} size={12} />{sourceCondition[s.condition]}</span></td>
                  <td data-l="最后成功">{s.last_ok_at ? <Ago t={s.last_ok_at} /> : "从未"}</td>
                  <td data-l="周期" className="num">{s.interval_s ? `${s.interval_s} 秒` : "—"}</td>
                  <td data-l="错误">{s.last_error || <span className="muted">无</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>
      <Inbox />
      <Panel title="审计" note="Agent 的每次调用都留记录">
        <div className="table-wrap">
          <table className="table table-tight">
            <thead><tr><th>时间</th><th>身份</th><th>动作</th><th>对象</th><th>结果</th></tr></thead>
            <tbody>
              {(audit.data?.items ?? []).map((a) => (
                <tr key={a.id}>
                  <td data-l="时间"><Ago t={a.ts} /></td>
                  <td data-l="身份">{a.principal}</td>
                  <td data-l="动作"><code>{a.action}</code></td>
                  <td data-l="对象">{a.target || "—"}</td>
                  <td data-l="结果">{a.result}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {audit.data && audit.data.items.length === 0 && <Empty title="还没有审计记录" />}
      </Panel>
    </>
  );
}
