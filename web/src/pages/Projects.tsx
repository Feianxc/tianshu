import { useMemo, useState } from "react";
import { useApp } from "../app";
import { useData } from "../lib";
import type { Card } from "../types";
import { Ago, Empty, Failure, Lamp, StageRail, Unconfirmed } from "../ui";
import { EntityEditor } from "./Editor";

const groups: [string, string[]][] = [
  ["进行中", ["building", "verifying", "planned"]],
  ["运行维护", ["delivered", "maintaining"]],
  ["想法", ["idea"]],
  ["已暂停", ["paused"]],
  ["已归档", ["archived"]],
];

function Row({ c }: { c: Card }) {
  const { spaceName } = useApp();
  const p = c.project!;
  return (
    <a className={`prow ${p.update_stale ? "prow-quiet" : ""}`} href={`#/e/${c.id}`}>
      <span className={`pri pri-${p.priority}`}>{p.priority || "—"}</span>
      <span className="prow-main">
        <span className="prow-title">
          <strong>{c.name}</strong>
          {!c.confirmed && <Unconfirmed />}
          {p.update_stale && <span className="tag">两周没有动静</span>}
        </span>
        {p.next_step ? (
          <span className="prow-next">{p.next_step}</span>
        ) : ["building", "verifying", "planned"].includes(p.stage) ? (
          <span className="prow-next prow-missing">没有写下一步</span>
        ) : (
          c.summary && <span className="prow-next">{c.summary}</span>
        )}
      </span>
      <span className="prow-space">{spaceName(c.workspace)}</span>
      <StageRail stage={p.stage} />
      <span className="prow-run" title={c.health.reason}>
        {p.services > 0 ? <><Lamp state={c.health.state} freshness={c.health.freshness} size={12} />{p.services_up}/{p.services} 服务</> : <span className="muted">无运行服务</span>}
      </span>
      {p.blockers > 0 ? <span className="tag tag-blocked">阻塞 {p.blockers}</span> : <span />}
      <span className="prow-when">{p.last_activity ? <Ago t={p.last_activity} /> : "—"}</span>
    </a>
  );
}

export function ProjectsPage() {
  const { space, can } = useApp();
  const { data, error, reload } = useData<{ items: Card[] }>(`/api/v1/entities?kind=project&limit=500${space ? `&workspace=${space}` : ""}`, 30000);
  const [adding, setAdding] = useState(false);
  const byGroup = useMemo(() => {
    const items = data?.items ?? [];
    return groups.map(([label, stages]) => {
      const list = items.filter((c) => stages.includes(c.project?.stage || "idea"));
      list.sort((a, b) => (a.project?.priority || "P9").localeCompare(b.project?.priority || "P9") || stages.indexOf(a.project!.stage) - stages.indexOf(b.project!.stage));
      return [label, list] as const;
    }).filter(([, l]) => l.length > 0);
  }, [data]);
  if (error && !data) return <Failure error={error} retry={reload} />;
  return (
    <>
      <header className="page-head">
        <div>
          <h1>项目</h1>
          <p>阶段、下一步和阻塞由你维护，运行状态来自关联的服务。代码提交多不代表项目完成，这里不算完成百分比。</p>
        </div>
        {can("write") && <button className="primary" onClick={() => setAdding(true)}>记下一个想法</button>}
      </header>
      {data && byGroup.length === 0 && <Empty title="这个空间还没有项目" hint="没有仓库的想法也可以先记下来。" />}
      {byGroup.map(([label, list]) => (
        <section className="pgroup" key={label}>
          <h2>{label}<span>{list.length}</span></h2>
          <div className="prows">{list.map((c) => <Row key={c.id} c={c} />)}</div>
        </section>
      ))}
      {adding && <EntityEditor kind="project" onClose={() => setAdding(false)} onSaved={reload} />}
    </>
  );
}

