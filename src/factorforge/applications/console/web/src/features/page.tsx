import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  checkResponse,
  query,
  record,
  request,
  rows,
  type Json,
  type Panel,
  type Response,
  type Selection,
} from "../api/client";
import { Button } from "../components/button";
import { DataTable, DataValue } from "../components/data-table";
import { label } from "../schemas/display";
const MarketChart = lazy(() =>
  import("./market-chart").then((module) => ({ default: module.MarketChart })),
);
import { useSession, useVisible } from "./session";
export const pages = [
  ["overview", "总览", "◈"],
  ["market", "行情", "⌁"],
  ["events", "事件与评分", "◎"],
  ["sentiment", "情绪组成", "◒"],
  ["decisions", "决策与目标", "↗"],
  ["execution", "执行与账务", "≋"],
  ["cases", "案例与归因", "▣"],
  ["learning", "参数学习", "◇"],
  ["operations", "采集与运维", "⏱"],
  ["reports", "周期报告", "▤"],
  ["audit", "审计追溯", "⌕"],
] as const;
const descriptions: Record<string, string> = {
  overview: "从实际事实开始，分别查看框架判断与应用状态。",
  market: "产品实际记录的行情，公开、可用与观察时间分别保留。",
  events: "追溯事实版本、证据和矢量评分；准入与评分生产分别展示。",
  sentiment: "查看框架已记录的贡献和消耗，不推演未记录的历史。",
  decisions: "目标送达、交易受理与实际成交分别展示。",
  execution: "持仓、订单、保护及费用，以交易层实际账务为准。",
  cases: "区分成熟度、标签状态、真实收益和研究反事实。",
  learning: "查看已记录的发布、拒绝、冻结和回滚，不增加批准门。",
  operations: "各任务分区和预算独立展示；未知结果保留未知。",
  reports: "已保存的周期记录与原因，读取不会启动分析。",
  audit: "按来源查看历史关联，各区独立分页。",
};
function merge(old: Response | undefined, next: Response): Response {
  if (!old) return next;
  return {
    ...next,
    panels: next.panels.map((p) => {
      if (p.state !== "UNAVAILABLE") return p;
      const prior = old.panels.find(
        (x) =>
          x.source === p.source &&
          (x.state === "AVAILABLE" ||
            x.state === "STALE" ||
            x.state === "EMPTY"),
      );
      return prior
        ? {
            ...p,
            state: "STALE",
            data: prior.data,
            fetched_at: prior.fetched_at,
            observed_at: prior.observed_at,
            source_version: prior.source_version,
            snapshot_version: prior.snapshot_version,
            cursor: null,
          }
        : p;
    }),
  };
}
export function PanelView({
  panel,
  onDetail,
  onNext,
}: {
  panel: Panel;
  onDetail?: (r: Record<string, Json>) => void;
  onNext?: (p: Panel) => void;
}) {
  const items = rows(panel.data);
  const envelope = record(panel.data);
  const content =
    envelope.data !== null &&
    typeof envelope.data === "object" &&
    !Array.isArray(envelope.data)
      ? record(envelope.data)
      : envelope;
  const age = Math.max(
    0,
    Math.floor((Date.now() - Date.parse(panel.fetched_at)) / 1000),
  );
  return (
    <section className="panel" aria-label={label(panel.source)}>
      <div className="panel-head">
        <div>
          <p className="eyebrow">{panel.source}</p>
          <h2>{label(panel.source)}</h2>
        </div>
        <span className={`badge state-${panel.state}`}>
          {panel.state} · {label(panel.state)}
        </span>
      </div>
      <div className="metadata">
        <span>
          读取 {panel.fetched_at} · {age} 秒前
        </span>
        <span>事实时间 {panel.observed_at ?? "未记录"}</span>
        {panel.snapshot_version && <span>快照 {panel.snapshot_version}</span>}
        {panel.source_version && <span>来源 {panel.source_version}</span>}
      </div>
      {panel.retry_at && (
        <p className="metadata">供应商允许重试时间 {panel.retry_at}</p>
      )}
      {panel.code && (
        <p role="status" className="error">
          {panel.code}
          {panel.state === "STALE" ? " · 以下为最近成功结果" : ""}
        </p>
      )}
      {items.length > 0 ? (
        <DataTable data={items} onDetail={onDetail} />
      ) : panel.data !== null && panel.state !== "EMPTY" ? (
        <dl className="key-values">
          {Object.entries(content)
            .filter(
              ([k]) =>
                ![
                  "schema_version",
                  "environment",
                  "instance_id",
                  "source_version",
                  "snapshot_version",
                  "cursor",
                ].includes(k),
            )
            .map(([k, v]) => (
              <div key={k}>
                <dt>{label(k)}</dt>
                <dd>
                  <DataValue value={v} />
                </dd>
              </div>
            ))}
        </dl>
      ) : (
        <p className="empty">
          {panel.state === "EMPTY"
            ? "本次读取的记录为空。"
            : "此区暂无可用数据。"}
        </p>
      )}
      {panel.cursor && onNext && (
        <Button variant="outline" size="sm" onClick={() => onNext(panel)}>
          下一页 · {label(panel.source)}
        </Button>
      )}
    </section>
  );
}
export function Page({ name }: { name: string }) {
  const {
    session,
    selection,
    generation,
    capabilities,
    invalidate,
    revalidate,
  } = useSession();
  const visible = useVisible();
  const client = useQueryClient();
  const [filters, setFilters] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [detail, setDetail] = useState<{ view: string; id: string } | null>(
    null,
  );
  const current = useRef({ generation, selection });
  current.current = { generation, selection };
  useEffect(() => {
    setFilters({});
    setDraft({});
    setDetail(null);
  }, [selection?.selection_id, selection?.binding_version, generation, name]);
  const key = [
    "console",
    session?.user_id,
    selection?.selection_id,
    selection?.binding_version,
    generation,
    name,
    filters,
  ];
  const range =
    name === "market" && (!filters.start || !filters.end || !filters.interval);
  const data = useQuery({
    queryKey: key,
    enabled: !!selection && !!session && !range,
    queryFn: async ({ signal }) => {
      const s = selection as Selection;
      const g = generation;
      const value = checkResponse(
        await request<Response>(query("/" + name, s, filters), signal),
        s,
      );
      if (
        current.current.generation !== g ||
        current.current.selection?.selection_id !== s.selection_id
      )
        throw new DOMException("Superseded", "AbortError");
      return merge(client.getQueryData<Response>(key), value);
    },
    refetchInterval: (state) =>
      visible && capabilities
        ? Math.max(
            capabilities.refresh_seconds * 1000,
            ...(state.state.data?.panels ?? []).map((p) =>
              p.retry_at ? Math.max(0, Date.parse(p.retry_at) - Date.now()) : 0,
            ),
          )
        : false,
    retry: (count, e) => {
      if (e instanceof ApiError && [401, 403, 409, 422].includes(e.status))
        return false;
      return (
        count < (capabilities?.max_retries ?? 0) &&
        (!(e instanceof ApiError) || e.retryable || e.status === 429)
      );
    },
    retryDelay: (attempt, error) =>
      error instanceof ApiError && error.status === 429
        ? error.retryAfter * 1000
        : ((capabilities?.refresh_seconds ?? 1) * 1000 * 2 ** attempt) /
          Math.max(1, (capabilities?.max_retries ?? 0) + 1),
  });
  useEffect(() => {
    const e = data.error;
    if (e instanceof ApiError && e.status === 401) invalidate();
    if (e instanceof ApiError && e.status === 403) {
      client.removeQueries({ queryKey: key });
      invalidate(true);
    }
    if (e instanceof ApiError && e.status === 409) {
      client.removeQueries({ queryKey: key });
      void revalidate();
    }
  }, [data.error]);
  const title = pages.find((x) => x[0] === name)?.[1] ?? name;
  if (!selection)
    return (
      <div className="page">
        <p className="eyebrow">SCOPE REQUIRED</p>
        <h1>先选择允许查看的运行</h1>
        <p className="muted">选择只改变展示范围。对象失效后请重新选择。</p>
      </div>
    );
  function open(row: Record<string, Json>) {
    const id = row.event_id ?? row.case_id ?? row.evidence_id;
    if (typeof id === "string")
      setDetail({
        view: row.event_id ? "events" : row.case_id ? "cases" : "evidence",
        id,
      });
  }
  async function exportData(format: string) {
    if (!selection) return;
    try {
      const params = new URLSearchParams({
        ...filters,
        selection_id: selection.selection_id,
        view: name,
        format,
      });
      const r = await fetch("/api/v2/console/export?" + params, {
        credentials: "include",
        cache: "no-store",
      });
      if (!r.ok) {
        const value = await r.json();
        throw new ApiError(r.status, value.code, false, 0);
      }
      const blob = await r.blob();
      if (current.current.generation !== generation) return;
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `factorforge-${name}.${format}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) invalidate();
      if (e instanceof ApiError && e.status === 403) invalidate(true);
    }
  }
  return (
    <div className="page">
      <div className="page-title">
        <div>
          <p className="eyebrow">OBSERVE / TRACE</p>
          <h1>{title}</h1>
          <p className="muted">{descriptions[name]}</p>
        </div>
        <div className="page-actions">
          <Button
            variant="outline"
            disabled={data.isFetching || range}
            onClick={() => data.refetch()}
          >
            ↻ 刷新
          </Button>
          {session?.capabilities.includes("export") && (
            <details className="export">
              <summary>导出 ↓</summary>
              <Button variant="ghost" onClick={() => exportData("json")}>
                JSON
              </Button>
              <Button variant="ghost" onClick={() => exportData("csv")}>
                CSV
              </Button>
            </details>
          )}
        </div>
      </div>
      {[
        "market",
        "events",
        "reports",
        "audit",
        "learning",
        "operations",
      ].includes(name) && (
        <form
          className="filters"
          onSubmit={(e) => {
            e.preventDefault();
            setFilters({ ...draft });
            setDetail(null);
          }}
        >
          {name === "market" ? (
            <>
              <label>
                周期
                <select
                  aria-label="周期"
                  value={draft.interval ?? ""}
                  onChange={(e) =>
                    setDraft({ ...draft, interval: e.target.value })
                  }
                >
                  <option value="">选择周期</option>
                  {["1m", "5m", "15m", "1h", "1d"].map((x) => (
                    <option key={x}>{x}</option>
                  ))}
                </select>
              </label>
              <label>
                起始 UTC
                <input
                  aria-label="起始 UTC"
                  placeholder="2026-10-07T00:00:00Z"
                  value={draft.start ?? ""}
                  onChange={(e) =>
                    setDraft({ ...draft, start: e.target.value })
                  }
                />
              </label>
              <label>
                结束 UTC
                <input
                  aria-label="结束 UTC"
                  placeholder="2026-10-08T00:00:00Z"
                  value={draft.end ?? ""}
                  onChange={(e) => setDraft({ ...draft, end: e.target.value })}
                />
              </label>
            </>
          ) : name === "operations" ? (
            <>
              <label>
                队列
                <select
                  value={draft.queue_kind ?? ""}
                  onChange={(e) =>
                    setDraft({ ...draft, queue_kind: e.target.value })
                  }
                >
                  <option value="">全部获准分区</option>
                  <option>RESEARCH</option>
                  <option>{selection.environment}</option>
                </select>
              </label>
              <label>
                任务状态
                <select
                  value={draft.state ?? ""}
                  onChange={(e) =>
                    setDraft({ ...draft, state: e.target.value })
                  }
                >
                  <option value="">全部</option>
                  {[
                    "QUEUED",
                    "RUNNING",
                    "COMPLETED",
                    "ABSTAINED",
                    "FAILED",
                    "EXPIRED",
                  ].map((x) => (
                    <option key={x}>{x}</option>
                  ))}
                </select>
              </label>
            </>
          ) : (
            <>
              <label>
                起始 UTC
                <input
                  value={draft.from ?? ""}
                  onChange={(e) => setDraft({ ...draft, from: e.target.value })}
                  placeholder="可选，完整 UTC 时间"
                />
              </label>
              <label>
                结束 UTC
                <input
                  value={draft.to ?? ""}
                  onChange={(e) => setDraft({ ...draft, to: e.target.value })}
                  placeholder="可选，完整 UTC 时间"
                />
              </label>
            </>
          )}
          <Button type="submit" variant="outline">
            应用范围
          </Button>
          {filters.source && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => setFilters({ ...draft })}
            >
              返回首批记录
            </Button>
          )}
        </form>
      )}
      {range && (
        <p className="empty">选择产品周期和 UTC 时间范围后读取最终 K 线。</p>
      )}
      {data.isPending && !range && <p role="status">正在读取各来源…</p>}
      {data.error && (
        <p role="alert" className="error">
          {data.error.message} · 当前请求失败
          {data.data ? "，下方保留最近成功结果" : ""}
        </p>
      )}
      {name === "overview" && data.data && (
        <div className="summary-grid">
          {[
            ["equity", "实际权益"],
            ["available_margin", "可用保证金"],
            ["net", "情绪净值"],
            ["recovery_state", "恢复状态"],
          ].map(([key, title]) => {
            const panel = data.data.panels.find((p) => key in record(p.data));
            const value = panel ? record(panel.data)[key] : null;
            return (
              <div className="summary-card" key={key}>
                <p>{title}</p>
                <strong>
                  <DataValue value={value} />
                </strong>
                <small>
                  {panel ? label(panel.source) : "未记录"}
                  {panel?.state === "STALE" ? " · 陈旧结果" : ""}
                </small>
              </div>
            );
          })}
        </div>
      )}
      {name === "market" && data.data && (
        <section className="panel">
          <div className="panel-head">
            <h2>{selection.instrument_key.instrument_id} · 最终 K 线</h2>
            <span className="badge">产品实际数据</span>
          </div>
          <Suspense fallback={<p role="status">正在装载图表…</p>}>
            <MarketChart
              candles={rows(
                data.data.panels.find((p) => p.source === "candles")?.data ??
                  null,
              )}
            />
          </Suspense>
        </section>
      )}
      <div className="panel-grid">
        {data.data?.panels.map((p) => (
          <PanelView
            key={p.source}
            panel={p}
            onDetail={["events", "cases"].includes(name) ? open : undefined}
            onNext={(p) =>
              setFilters({
                ...filters,
                source: p.source,
                cursor: p.cursor ?? "",
              })
            }
          />
        ))}
      </div>
      {detail && (
        <Detail
          selection={selection}
          generation={generation}
          view={detail.view}
          id={detail.id}
          onClose={() => setDetail(null)}
        />
      )}
    </div>
  );
}
function Detail({
  selection,
  generation,
  view,
  id,
  onClose,
}: {
  selection: Selection;
  generation: number;
  view: string;
  id: string;
  onClose: () => void;
}) {
  const { invalidate } = useSession();
  const [revision, setRevision] = useState("");
  const [evidence, setEvidence] = useState<string | null>(null);
  const panel = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    panel.current?.showModal();
    return () => panel.current?.close();
  }, []);
  const data = useQuery({
    queryKey: [
      "console-detail",
      selection.selection_id,
      selection.binding_version,
      generation,
      view,
      id,
      revision,
    ],
    queryFn: async ({ signal }) =>
      checkResponse(
        await request<Response>(
          query(
            `/${view}/${encodeURIComponent(id)}`,
            selection,
            revision ? { revision } : {},
          ),
          signal,
        ),
        selection,
      ),
    retry: false,
  });
  const original = useQuery({
    queryKey: [
      "console-original",
      selection.selection_id,
      selection.binding_version,
      generation,
      evidence,
    ],
    enabled: !!evidence,
    queryFn: async ({ signal }) =>
      checkResponse(
        await request<Response>(
          query(
            "/evidence/" + encodeURIComponent(evidence as string),
            selection,
          ),
          signal,
        ),
        selection,
      ),
    retry: false,
  });
  useEffect(() => {
    for (const error of [data.error, original.error])
      if (error instanceof ApiError) {
        if (error.status === 401) invalidate();
        if (error.status === 403) {
          invalidate(true);
          onClose();
        }
      }
  }, [data.error, original.error]);
  const refs = new Set<string>();
  for (const p of data.data?.panels ?? [])
    for (const r of rows(p.data)) {
      const row = record(r);
      if (Array.isArray(row.evidence_refs))
        for (const x of row.evidence_refs) {
          if (typeof x === "string") refs.add(x);
          else {
            const evidence = record(x).evidence_id;
            if (typeof evidence === "string") refs.add(evidence);
          }
        }
    }
  return (
    <dialog ref={panel} className="detail-dialog" onCancel={onClose}>
      <div className="detail-top">
        <div>
          <p className="eyebrow">DETAIL / {selection.environment}</p>
          <h2>{id}</h2>
        </div>
        <Button variant="outline" onClick={onClose}>
          关闭
        </Button>
      </div>
      {view === "events" && (
        <label>
          事实版本（留空为全部）
          <input
            value={revision}
            onChange={(e) => setRevision(e.target.value)}
            inputMode="numeric"
          />
        </label>
      )}
      {data.isPending && <p>正在读取…</p>}
      {data.error && (
        <p role="alert" className="error">
          {data.error.message}
        </p>
      )}
      {data.data?.panels.map((p) => (
        <PanelView key={p.source} panel={p} />
      ))}
      {refs.size > 0 && (
        <div className="evidence-links">
          <h3>证据与原文权限</h3>
          {[...refs].map((id) => (
            <Button key={id} variant="outline" onClick={() => setEvidence(id)}>
              {id}
            </Button>
          ))}
        </div>
      )}
      {original.error && (
        <p role="alert" className="error">
          {original.error.message}
        </p>
      )}
      {original.data?.panels.map((p) => (
        <PanelView key={p.source} panel={p} />
      ))}
    </dialog>
  );
}
