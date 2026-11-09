import { useState, type FormEvent } from "react";
import { useApp } from "../app";
import { api, storageLabel, str, useData } from "../lib";
import type { Card, Principal } from "../types";
import { Ago, Drawer, Empty, Failure, Panel, Unconfirmed } from "../ui";

const kindName: Record<string, string> = { human: "人", agent: "Agent", collector: "采集器" };

function NewIdentity({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const { workspaces } = useApp();
  const [kind, setKind] = useState("agent");
  const [name, setName] = useState("");
  const [spaces, setSpaces] = useState<string[]>([]);
  const [evidence, setEvidence] = useState(false);
  const [ttl, setTtl] = useState(90);
  const [devices, setDevices] = useState("");
  const [token, setToken] = useState("");
  const [err, setErr] = useState("");
  const toggle = (s: string) => setSpaces((l) => (l.includes(s) ? l.filter((x) => x !== s) : [...l, s]));
  const save = async (e: FormEvent) => {
    e.preventDefault();
    setErr("");
    const scopes = kind === "collector" ? ["ingest"] : evidence ? ["read", "evidence"] : ["read"];
    try {
      const r = await api.post<{ token: string }>("/api/v1/principals", {
        kind, name, scopes, workspaces: spaces, ttl_days: ttl,
        devices: kind === "collector" ? devices.split(/[,\s]+/).filter(Boolean) : [],
      });
      setToken(r.token);
      onSaved();
    } catch (x) {
      setErr((x as Error).message);
    }
  };
  if (token) {
    return (
      <Drawer title="令牌只显示这一次" onClose={onClose}>
        <p className="note">现在复制并存进密匣。关闭后再也看不到，只能吊销重建。</p>
        <code className="token">{token}</code>
        <footer className="drawer-foot"><button className="primary" onClick={onClose}>已保存，关闭</button></footer>
      </Drawer>
    );
  }
  return (
    <Drawer title="新建身份" onClose={onClose}>
      <form className="form" onSubmit={save}>
        <div className="form-row">
          <label><span>类型</span>
            <select value={kind} onChange={(e) => setKind(e.target.value)}>
              <option value="agent">Agent（只读）</option>
              <option value="collector">采集器</option>
            </select>
          </label>
          <label><span>有效天数</span><input type="number" min={1} max={365} value={ttl} onChange={(e) => setTtl(Number(e.target.value))} /></label>
        </div>
        <label><span>名称</span><input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 claude-code" required /></label>
        {kind === "agent" ? (
          <>
            <fieldset>
              <legend>可以看到的空间</legend>
              {workspaces.map((w) => (
                <label className="check" key={w.id}>
                  <input type="checkbox" checked={spaces.includes(w.slug)} onChange={() => toggle(w.slug)} />
                  <span>{w.name}</span>
                  <small>{w.cloud_policy === "allow" ? "完整字段" : w.cloud_policy === "summary" ? "脱敏摘要" : "禁止云端"}</small>
                </label>
              ))}
            </fieldset>
            <label className="check"><input type="checkbox" checked={evidence} onChange={(e) => setEvidence(e.target.checked)} /><span>允许提交验收证据</span></label>
          </>
        ) : (
          <label><span>可上报的设备 slug</span><input value={devices} onChange={(e) => setDevices(e.target.value)} placeholder="my-server, laptop" required /></label>
        )}
        {err && <p className="form-error" role="alert">{err}</p>}
        <footer>
          <button className="primary" disabled={!name.trim() || (kind === "agent" && spaces.length === 0)}>创建</button>
          <button type="button" className="ghost" onClick={onClose}>取消</button>
        </footer>
      </form>
    </Drawer>
  );
}

function Identities() {
  const { data, error, reload } = useData<{ items: Principal[] }>("/api/v1/principals");
  const [adding, setAdding] = useState(false);
  const revoke = async (p: Principal) => {
    await api.del(`/api/v1/principals/${p.id}`);
    reload();
  };
  if (error && !data) return <Failure error={error} retry={reload} />;
  const live = (data?.items ?? []).filter((p) => !p.revoked_at);
  return (
    <Panel title="身份" note="人、Agent、采集器各用各的令牌，互不继承权限" action={<button className="primary" onClick={() => setAdding(true)}>新建身份</button>}>
      <div className="table-wrap">
        <table className="table">
          <thead><tr><th>名称</th><th>类型</th><th>权限</th><th>空间或设备</th><th>最后使用</th><th /></tr></thead>
          <tbody>
            {live.map((p) => (
              <tr key={p.id}>
                <td data-l="名称">{p.name}<small className="num">{p.token_prefix}…</small></td>
                <td data-l="类型">{kindName[p.kind] ?? p.kind}</td>
                <td data-l="权限">{p.scopes.join("、")}</td>
                <td data-l="空间或设备">{p.kind === "collector" ? p.devices.join("、") : p.workspaces.join("、")}</td>
                <td data-l="最后使用">{p.last_used_at ? <Ago t={p.last_used_at} /> : "从未"}</td>
                <td data-l="">{p.kind !== "human" && <button className="link danger" onClick={() => revoke(p)}>吊销</button>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {adding && <NewIdentity onClose={() => setAdding(false)} onSaved={reload} />}
    </Panel>
  );
}

export function AccessPage() {
  const { space, can, spaceName } = useApp();
  const { data, error, reload } = useData<{ items: Card[] }>(`/api/v1/entities?kind=credential&limit=500${space ? `&workspace=${space}` : ""}`);
  if (error && !data) return <Failure error={error} retry={reload} />;
  const items = data?.items ?? [];
  const url = `${location.origin}/mcp`;
  return (
    <>
      <header className="page-head">
        <div>
          <h1>访问</h1>
          <p>这里记录每个系统的钥匙放在哪、谁能用。天枢不保存密码和私钥本身。</p>
        </div>
      </header>
      <Panel title="凭据引用" note={`${items.length} 条`}>
        {data && items.length === 0 ? <Empty title="还没有凭据引用" /> : (
          <div className="table-wrap">
            <table className="table">
              <thead><tr><th>名称</th><th>空间</th><th>存放方式</th><th>使用方式</th><th>位置</th></tr></thead>
              <tbody>
                {items.map((c) => (
                  <tr key={c.id}>
                    <td data-l="名称"><a href={`#/e/${c.id}`}>{c.name}</a>{c.findings > 0 && <span className="tag tag-blocked">{c.findings} 项风险</span>}{!c.confirmed && <> <Unconfirmed /></>}</td>
                    <td data-l="空间">{spaceName(c.workspace)}</td>
                    <td data-l="存放方式">{storageLabel[str(c.attrs.storage)] ?? str(c.attrs.storage)}</td>
                    <td data-l="使用方式">{str(c.attrs.mode)}</td>
                    <td data-l="位置"><small className="path">{str(c.attrs.locator)}</small></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
      {can("admin") && <Identities />}
      {can("admin") && (
        <Panel title="在其他设备上登录">
          <p className="note">在手机或另一台电脑打开天枢，登录页点“让电脑批准登录”。这里会在页面右上角弹出请求，核对两边的代码一致后批准，那台设备就以你的身份登录，可以选 12 小时或 7 天。请求 5 分钟内不批准就作废，每一次批准都记在审计里。</p>
        </Panel>
      )}
      <Panel title="让 Agent 接入">
        <div className="connect">
          <p>MCP 地址 <code>{url}</code>。先在上面新建一个 Agent 身份，把令牌放进环境变量 WANGLOU_TOKEN。</p>
          <h3>Claude Code</h3>
          <pre>{`claude mcp add --transport http wanglou ${url} --header "Authorization: Bearer $WANGLOU_TOKEN"`}</pre>
          <h3>Codex</h3>
          <pre>{`[mcp_servers.wanglou]\nurl = "${url}"\nbearer_token_env_var = "WANGLOU_TOKEN"`}</pre>
          <h3>OpenAI dots、ChatGPT、Claude 网页版</h3>
          <p>这些云端客户端要求公网 HTTPS 和 OAuth，需要先部署到常开服务器。dots 能否调用自定义 MCP，官方文档没有写明，要实测。</p>
        </div>
      </Panel>
    </>
  );
}
