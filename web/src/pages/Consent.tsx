import { useState } from "react";
import { useApp } from "../app";
import { api, useData } from "../lib";
import { Failure, Lamp, Panel } from "../ui";

interface Req {
  client_name: string;
  client_id: string;
  redirect_host: string;
  scope: string;
}

// OAuth 授权确认。可见范围由你在这里勾选，不接受客户端自己申请的范围。
export function ConsentPage({ id }: { id: string }) {
  const { workspaces } = useApp();
  const { data, error } = useData<Req>(`/api/v1/oauth/requests/${id}`);
  const [spaces, setSpaces] = useState<string[]>([]);
  const [evidence, setEvidence] = useState(false);
  const [logs, setLogs] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  if (error && !data) return <Failure error={new Error("这个授权请求已过期或已处理，请回到客户端重新发起。")} />;
  if (!data) return <div className="loading">读取中</div>;
  const toggle = (s: string) => setSpaces((l) => (l.includes(s) ? l.filter((x) => x !== s) : [...l, s]));
  const decide = async (approve: boolean) => {
    setBusy(true);
    setErr("");
    try {
      const r = await api.post<{ redirect: string }>(`/api/v1/oauth/requests/${id}`, { approve, workspaces: spaces, evidence, logs });
      location.href = r.redirect;
    } catch (x) {
      setErr((x as Error).message);
      setBusy(false);
    }
  };
  return (
    <div className="consent">
      <Panel title="授权 Agent 接入">
        <p className="consent-who"><b>{data.client_name}</b> 想读取天枢的数据，授权后会跳转到 <code>{data.redirect_host}</code>。</p>
        <p className="note">只有你认识这个地址时才授权。随时可以在“访问”页吊销。</p>
        <fieldset>
          <legend>允许它看到的空间</legend>
          {workspaces.map((w) => (
            <label className="check" key={w.id}>
              <input type="checkbox" checked={spaces.includes(w.slug)} onChange={() => toggle(w.slug)} disabled={w.cloud_policy === "deny"} />
              <span>{w.name}</span>
              <small>{w.cloud_policy === "allow" ? "完整字段" : w.cloud_policy === "summary" ? "脱敏摘要" : "禁止进云"}</small>
            </label>
          ))}
        </fieldset>
        <label className="check"><input type="checkbox" checked={evidence} onChange={(e) => setEvidence(e.target.checked)} /><span>允许提交验收证据</span></label>
        <label className="check"><input type="checkbox" checked={logs} onChange={(e) => setLogs(e.target.checked)} /><span>允许读取日志</span><small>只对完整字段的空间生效，用来定位问题</small></label>
        <ul className="consent-list">
          <li><Lamp state="ok" size={12} />可以查询状态、项目、问题和凭据引用</li>
          <li><Lamp state="unknown" size={12} />不能读取任何密码或密钥</li>
          <li><Lamp state={logs ? "ok" : "unknown"} size={12} />{logs ? "可以读取警告和错误日志（已隐去疑似密钥）" : "只能看到各单元的错误和警告数量，看不到日志原文"}</li>
          <li><Lamp state="unknown" size={12} />不能操作服务器</li>
        </ul>
        {err && <p className="form-error" role="alert">{err}</p>}
        <footer className="consent-foot">
          <button className="primary" disabled={busy || spaces.length === 0} onClick={() => decide(true)}>授权</button>
          <button disabled={busy} onClick={() => decide(false)}>拒绝</button>
        </footer>
      </Panel>
    </div>
  );
}
