import { useState, type FormEvent } from "react";
import { useApp } from "../app";
import { api, deviceTypeLabel, stageLabel, str } from "../lib";
import type { Card, Kind } from "../types";
import { Drawer } from "../ui";

const slugOf = (name: string) => {
  const base = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  return base || `item-${Date.now().toString(36)}`;
};

// 新建和编辑共用的表单。这里只有项目管理需要的字段；密码和密钥不能写进目录，
// 服务端也会拒绝。
export function EntityEditor({ kind, entity, onClose, onSaved }: { kind: Kind; entity?: Card; onClose: () => void; onSaved: () => void }) {
  const { workspaces, space } = useApp();
  const [name, setName] = useState(entity?.name ?? "");
  const [summary, setSummary] = useState(entity?.summary ?? "");
  const [lifecycle, setLifecycle] = useState(entity?.lifecycle ?? "active");
  const [workspace, setWorkspace] = useState(entity?.workspace ?? (space || workspaces[0]?.slug || ""));
  const a = entity?.attrs ?? {};
  const [stage, setStage] = useState(str(a.stage) || "idea");
  const [priority, setPriority] = useState(str(a.priority));
  const [nextStep, setNextStep] = useState(str(a.next_step));
  const [due, setDue] = useState(str(a.due));
  const [deviceType, setDeviceType] = useState(str(a.device_type) || "phone");
  const [availability, setAvailability] = useState(str(a.availability) || "manual");
  const [rotation, setRotation] = useState(str(a.rotation_due));
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // Editing sends only what changed. The form shows defaults for fields an object never
  // had; writing those back would, for example, turn a server into a manually logged phone.
  const initial: Record<string, string> = {
    stage: str(a.stage) || "idea", priority: str(a.priority), next_step: str(a.next_step), due: str(a.due),
    device_type: str(a.device_type) || "phone", availability: str(a.availability) || "manual", rotation_due: str(a.rotation_due),
  };
  const attrs = () => {
    const out: Record<string, unknown> = {};
    const set = (k: string, v: string) => {
      if (entity && v === initial[k]) return;
      out[k] = v === "" ? null : v;
    };
    if (kind === "project") {
      set("stage", stage); set("priority", priority); set("next_step", nextStep); set("due", due);
    } else if (kind === "device") {
      set("device_type", deviceType); set("availability", availability);
    } else if (kind === "credential") {
      set("rotation_due", rotation);
    }
    return out;
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      if (entity) {
        const ws = workspaces.find((w) => w.slug === workspace);
        await api.patch(`/api/v1/entities/${entity.id}`, { name, summary, lifecycle, workspace_id: ws?.id, attrs: attrs(), rev: entity.rev, confirmed: true });
      } else {
        const clean = Object.fromEntries(Object.entries(attrs()).filter(([, v]) => v !== null));
        await api.post("/api/v1/entities", { kind, workspace, slug: slugOf(name), name, summary, lifecycle, attrs: clean });
      }
      onSaved();
      onClose();
    } catch (x) {
      setErr((x as Error).message);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer title={entity ? `编辑 ${entity.name}` : kind === "project" ? "记下一个项目或想法" : "登记设备"} onClose={onClose}>
      <form className="form" onSubmit={save}>
        <label><span>名称</span><input value={name} onChange={(e) => setName(e.target.value)} required /></label>
        <label><span>简介</span><textarea rows={2} value={summary} onChange={(e) => setSummary(e.target.value)} /></label>
        <div className="form-row">
          <label><span>空间</span>
            <select value={workspace} onChange={(e) => setWorkspace(e.target.value)}>
              {workspaces.map((w) => <option key={w.id} value={w.slug}>{w.name}</option>)}
            </select>
          </label>
          <label><span>生命周期</span>
            <select value={lifecycle} onChange={(e) => setLifecycle(e.target.value)}>
              <option value="active">运行中</option><option value="paused">已暂停</option>
              <option value="planned">计划中</option><option value="retired">已退役</option>
            </select>
          </label>
        </div>
        {kind === "project" && (
          <>
            <div className="form-row">
              <label><span>阶段</span>
                <select value={stage} onChange={(e) => setStage(e.target.value)}>
                  {Object.entries(stageLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
                </select>
              </label>
              <label><span>优先级</span>
                <select value={priority} onChange={(e) => setPriority(e.target.value)}>
                  <option value="">未定</option><option>P0</option><option>P1</option><option>P2</option><option>P3</option>
                </select>
              </label>
              <label><span>计划日期</span><input type="date" value={due} onChange={(e) => setDue(e.target.value)} /></label>
            </div>
            <label><span>下一步</span><textarea rows={2} value={nextStep} onChange={(e) => setNextStep(e.target.value)} placeholder="下次回来时第一件要做的事" /></label>
          </>
        )}
        {kind === "device" && (
          <div className="form-row">
            <label><span>类型</span>
              <select value={deviceType} onChange={(e) => setDeviceType(e.target.value)}>
                {Object.entries(deviceTypeLabel).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
              </select>
            </label>
            <label><span>在线规律</span>
              <select value={availability} onChange={(e) => setAvailability(e.target.value)}>
                <option value="always_on">一直在线</option><option value="intermittent">间歇在线</option>
                <option value="event">事件上报</option><option value="manual">手工登记</option>
              </select>
            </label>
          </div>
        )}
        {kind === "credential" && (
          <label><span>计划轮换日期</span><input type="date" value={rotation} onChange={(e) => setRotation(e.target.value)} /></label>
        )}
        {err && <p className="form-error" role="alert">{err}</p>}
        <footer>
          <button className="primary" disabled={busy || !name.trim()}>{entity ? "保存修改" : "保存"}</button>
          <button type="button" className="ghost" onClick={onClose}>取消</button>
        </footer>
      </form>
    </Drawer>
  );
}
