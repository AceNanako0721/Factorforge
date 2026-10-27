import { lazy, Suspense, useMemo } from "react";
import {
  record,
  rows,
  type Json,
  type Panel,
  type Selection,
} from "../api/client";
import { displayTime, useDisplay } from "./display-settings";
import { liveStateText, useLiveMarket } from "./live-market";
import type { PriceLine, TimeMarker } from "./market-chart";
import { PnlPanel } from "./pnl-panel";
const MarketChart = lazy(() =>
  import("./market-chart").then((module) => ({ default: module.MarketChart })),
);

// S4-017 / P4 design 16.3. Every value is a recorded lower-layer value; the
// distances and R multiples below are display conversions only.
export const dynamicSources = [
  "dynamic.status",
  "dynamic.judgments",
  "dynamic.actions",
  "account",
  "positions",
  "protections",
  "object",
  "candles",
];

// Rolling default range: 5-minute bars over 24 hours, timelines over 72 hours.
// v2.14.8: P1 keeps replaced generations as CLOSED; only the generation
// still guarding the position is drawn, verified first, then the newest.
export function currentProtection(protections: Record<string, Json>[], role: string) {
  return protections
    .filter((p) => p.role === role && p.state !== "CLOSED" && p.state !== "CANCELLED" && p.state !== "CANCEL_PENDING")
    .sort(
      (a, b) =>
        Number(b.state === "ACTIVE_VERIFIED") - Number(a.state === "ACTIVE_VERIFIED") ||
        Number(b.generation ?? 0) - Number(a.generation ?? 0),
    )[0];
}
export function dynamicRange(now = Date.now()): Record<string, string> {
  const minute = 60_000;
  const end = new Date(Math.ceil(now / (5 * minute)) * 5 * minute);
  const iso = (d: Date) => d.toISOString().replace(/\.\d{3}Z$/, "Z");
  return {
    interval: "5m",
    start: iso(new Date(end.getTime() - 24 * 60 * minute)),
    end: iso(end),
    from: iso(new Date(end.getTime() - 72 * 60 * minute)),
  };
}

function text(v: Json): string | null {
  return typeof v === "string" && v !== "" ? v : null;
}
function num(v: Json): number | null {
  const n = typeof v === "string" ? Number(v) : typeof v === "number" ? v : NaN;
  return Number.isFinite(n) ? n : null;
}
function fmt(n: number | null, digits = 2): string {
  return n === null ? "未记录" : n.toFixed(digits);
}
function pct(price: number | null, ref: number | null): string {
  if (price === null || ref === null || ref === 0) return "—";
  const p = ((price - ref) / ref) * 100;
  return `${p >= 0 ? "+" : ""}${p.toFixed(2)}%`;
}
const directionText: Record<string, string> = {
  LONG: "做多",
  SHORT: "做空",
  FLAT: "无方向",
};

function panelOf(panels: Panel[], source: string): Panel | undefined {
  return panels.find((p) => p.source === source);
}
function StateNote({ panel }: { panel?: Panel }) {
  if (!panel) return <small>未读取</small>;
  if (panel.state === "AVAILABLE") return null;
  return (
    <small className={`badge state-${panel.state}`}>
      {panel.state}
      {panel.code ? ` · ${panel.code}` : ""}
    </small>
  );
}

export function DynamicView({
  panels,
  selection,
}: {
  panels: Panel[];
  selection: Selection;
}) {
  const { zone } = useDisplay();
  const live = useLiveMarket(selection.selection_id);
  const t = (v: Json) => (typeof v === "string" ? displayTime(v, zone) : "未记录");
  const statusPanel = panelOf(panels, "dynamic.status");
  const status = record(record(statusPanel?.data ?? null).data ?? null);
  const batch = status.batch ? record(status.batch) : null;
  const chain = status.chain ? record(status.chain) : null;
  const baseline = record(status.baseline ?? null);
  const accountPanel = panelOf(panels, "account");
  const account = record(accountPanel?.data ?? null);
  const positionPanel = panelOf(panels, "positions");
  const position = rows(positionPanel?.data ?? null)
    .map(record)
    .find(
      (p) =>
        record(p.instrument_key).instrument_id ===
          selection.instrument_key.instrument_id && p.owner_id === selection.owner_id,
    );
  const protectionPanel = panelOf(panels, "protections");
  const protections = rows(protectionPanel?.data ?? null).map(record);
  const candlePanel = panelOf(panels, "candles");
  const candles = rows(candlePanel?.data ?? null);
  const lastCandle = candles
    .map(record)
    .filter((c) => c.final === true && typeof c.close_at === "string")
    .sort((a, b) => String(a.close_at).localeCompare(String(b.close_at)))
    .at(-1);
  const last = lastCandle ? num(lastCandle.close) : null;
  const judgmentsPanel = panelOf(panels, "dynamic.judgments");
  const judgments = rows(judgmentsPanel?.data ?? null).map(record);
  const actionsPanel = panelOf(panels, "dynamic.actions");
  const actions = rows(actionsPanel?.data ?? null).map(record);

  const entry = batch ? num(batch.entry) : null;
  const budget = batch ? num(batch.budget) : null;
  const unrealized = num(account.unrealized_pnl);
  const protectionOf = (role: string) => currentProtection(protections, role);
  // S4-019 reference layer: venue public push, display conversion only.
  const refMark = live.mark ? num(live.mark.price) : null;
  const qty = position ? num(position.quantity) : null;
  const avg = position ? num(position.average_entry) : null;
  const refPnl = refMark !== null && qty !== null && avg !== null ? qty * (refMark - avg) : null;
  const liveFresh = live.state === "LIVE";
  const liveNote =
    live.state === "UNAVAILABLE" && live.code === "LIVE_NOT_APPLICABLE"
      ? "不适用或未启用"
      : `${liveStateText[live.state]}${live.mark ? " · " + t(live.mark.at) : ""}`;
  const strategy = protectionOf("STRATEGY");
  const insurance = protectionOf("INSURANCE");
  const ladder: {
    name: string;
    price: number | null;
    source: string;
    note: string;
    color: string;
    dashed?: boolean;
  }[] = [];
  if (insurance)
    ladder.push({
      name: "保险止损",
      price: num(record(insurance.plan).trigger_price),
      source: "P1 交易所条件单",
      note: `${text(insurance.state) ?? "未记录"} · ${text(record(insurance.plan).trigger_kind) ?? ""}`,
      color: "#7a2b22",
      dashed: true,
    });
  if (strategy)
    ladder.push({
      name: "交易所策略止损",
      price: num(record(strategy.plan).trigger_price),
      source: "P1 交易所条件单",
      note: `${text(strategy.state) ?? "未记录"} · ${text(record(strategy.plan).trigger_kind) ?? ""}`,
      color: "#b15447",
    });
  if (batch) {
    ladder.push({
      name: "收盘止损",
      price: num(batch.close_stop),
      source: "P3 记录（完成的 K 线收盘越过才退出，不挂单）",
      note: batch.trailing === true ? "已启动移动止损" : "未移动",
      color: "#c27c1a",
      dashed: true,
    });
    ladder.push({
      name: "移动止损启动价（+1R）",
      price: num(batch.trail_trigger),
      source: "P3 记录换算",
      note: batch.transferred === true ? "已转入基线" : "未转基线",
      color: "#267b59",
      dashed: true,
    });
    ladder.push({
      name: "入场价",
      price: entry,
      source: "P3 批次",
      note: `${t(batch.opened_at)}`,
      color: "#55605a",
    });
  }
  ladder.push({
    name: "最新完成 K 线收盘",
    price: last,
    source: "P1 最终 K 线",
    note: lastCandle ? t(lastCandle.close_at) : "无 K 线",
    color: "#203a2c",
  });
  if (refMark !== null)
    ladder.push({
      name: "参考标记价",
      price: refMark,
      source: "交易所公开行情（参考，不是账务）",
      note: liveNote,
      color: "#2f5d8a",
    });
  ladder.sort((a, b) => (b.price ?? -Infinity) - (a.price ?? -Infinity));

  const lines: PriceLine[] = useMemo(
    () =>
      ladder
        .filter(
          (l) => l.price !== null && l.name !== "最新完成 K 线收盘" && l.name !== "参考标记价",
        )
        .map((l) => ({
          price: l.price as number,
          title: l.name,
          color: l.color,
          dashed: l.dashed,
        })),
    [JSON.stringify(ladder.map((l) => [l.name, l.price]))],
  );
  const markers: TimeMarker[] = useMemo(
    () =>
      judgments
        .filter((j) => j.ok === true && typeof j.at === "string")
        .map((j) => ({
          at: j.at as string,
          text: `${j.direction === "LONG" ? "多" : j.direction === "SHORT" ? "空" : "平"}${j.confidence ?? ""}`,
          color:
            j.direction === "LONG"
              ? "#267b59"
              : j.direction === "SHORT"
                ? "#b15447"
                : "#738071",
          shape:
            j.direction === "LONG"
              ? ("arrowUp" as const)
              : j.direction === "SHORT"
                ? ("arrowDown" as const)
                : ("circle" as const),
        })),
    [JSON.stringify(judgments.map((j) => [j.judgment_id, j.direction]))],
  );

  return (
    <div className="dynamic">
      <div className="summary-grid">
        <div className="summary-card">
          <p>仓位（P1）</p>
          <strong>
            {position
              ? `${num(position.quantity)! < 0 ? "做空" : "做多"} ${Math.abs(num(position.quantity) ?? 0)}`
              : "无持仓"}
          </strong>
          <small>
            均价 {position ? text(position.average_entry) : "—"} ·{" "}
            {position ? text(position.protection_state) : ""}
          </small>
          <StateNote panel={positionPanel} />
        </div>
        <div className="summary-card">
          <p>浮动盈亏（P1）</p>
          <strong>{fmt(unrealized)} USDT</strong>
          <small>
            {budget && unrealized !== null
              ? `约 ${(unrealized / budget).toFixed(2)}R（显示换算，本批风险预算 ${fmt(budget)}）`
              : "无批次风险预算"}
          </small>
          <StateNote panel={accountPanel} />
        </div>
        <div className={`summary-card live-card${liveFresh ? "" : " live-stale"}`} aria-label="参考标记价">
          <p>参考标记价 · 交易所公开行情</p>
          <strong>{refMark !== null ? live.mark!.price : "—"}</strong>
          <small>{liveNote}</small>
        </div>
        <div className={`summary-card live-card${liveFresh ? "" : " live-stale"}`} aria-label="参考浮动盈亏">
          <p>参考浮动盈亏（显示换算）</p>
          <strong>{refPnl !== null ? `${fmt(refPnl)} USDT` : "—"}</strong>
          <small>
            {refPnl !== null && budget
              ? `约 ${(refPnl / budget).toFixed(2)}R · P1 仓位 × (参考标记价 − 均价)，不含费用`
              : position
                ? "等待参考标记价"
                : "无持仓"}
          </small>
        </div>
        <div className="summary-card">
          <p>权益（P1）</p>
          <strong>{fmt(num(account.equity))} USDT</strong>
          <small>
            已实现 {fmt(num(account.realized_pnl))} · 手续费 {fmt(num(account.fees))} ·{" "}
            {text(account.risk_state) ?? ""}
          </small>
        </div>
        <div className="summary-card">
          <p>判断链（P3）</p>
          <strong>
            {chain
              ? `${directionText[String(chain.direction)] ?? chain.direction} · 信心 ${chain.confidence}`
              : "无"}
          </strong>
          <small>
            {chain
              ? `起于 ${t(chain.since)} · 含 ${chain.judgment_count} 次研判`
              : "当前没有活动的判断链"}
          </small>
          <StateNote panel={statusPanel} />
        </div>
      </div>

      <PnlPanel
        pnl={account.pnl}
        instrumentCode={`${selection.instrument_key.venue}:${selection.instrument_key.product}:${selection.instrument_key.instrument_id}`}
        time={t}
      />

      <section className="panel" aria-label="三层止损">
        <div className="panel-head">
          <div>
            <p className="eyebrow">STOP LADDER</p>
            <h2>三层止损与价格</h2>
          </div>
          <span className="badge">距离为显示换算</span>
        </div>
        {batch && (
          <p className="metadata">
            <span>
              档位 {String(batch.tier_hours)} 小时 · {text(batch.session)} · R{" "}
              {fmt(num(batch.r) !== null ? num(batch.r)! * 100 : null)}% · 缓冲{" "}
              {fmt(num(batch.buffer) !== null ? num(batch.buffer)! * 100 : null)}% ·
              回撤 {fmt(num(batch.trail) !== null ? num(batch.trail)! * 100 : null)}%
            </span>
            <span>最有利收盘 {text(batch.best_close)}</span>
            <span>批次 {text(batch.cycle_id)}</span>
          </p>
        )}
        <div className="table-scroll">
          <table className="ladder">
            <thead>
              <tr>
                <th>价位</th>
                <th>价格</th>
                <th>相对最新收盘</th>
                <th>相对参考</th>
                <th>来源</th>
                <th>状态</th>
              </tr>
            </thead>
            <tbody>
              {ladder.map((l) => (
                <tr key={l.name}>
                  <td>
                    <span className="swatch" style={{ background: l.color }} />
                    {l.name}
                  </td>
                  <td>{fmt(l.price)}</td>
                  <td>{l.name === "最新完成 K 线收盘" ? "—" : pct(l.price, last)}</td>
                  <td>{l.name === "参考标记价" ? "—" : pct(l.price, refMark)}</td>
                  <td>{l.source}</td>
                  <td>{l.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <h2>{selection.instrument_key.instrument_id} · 5 分钟最终 K 线（24 小时）</h2>
          <StateNote panel={candlePanel} />
        </div>
        <Suspense fallback={<p role="status">正在装载图表…</p>}>
          <MarketChart candles={candles} lines={lines} markers={markers} live={live.kline} />
        </Suspense>
        <p className="muted">
          箭头为研判时刻（空/多 + 信心），横线为入场价与各止损位（纵轴默认只按 K 线缩放，范围外的横线列在图下）；半透明的最后一根是交易所推送的未完成 K 线（参考），收盘后以 P1 最终 K 线为准；不画预测。
        </p>
      </section>

      <div className="dynamic-columns">
        <section className="panel" aria-label="研判时间线">
          <div className="panel-head">
            <div>
              <p className="eyebrow">dynamic.judgments</p>
              <h2>研判时间线（72 小时）</h2>
            </div>
            <StateNote panel={judgmentsPanel} />
          </div>
          {judgments.length === 0 && <p className="empty">未记录研判。</p>}
          <ol className="timeline">
            {judgments.map((j) => (
              <li key={String(j.judgment_id)} className={j.ok === true ? "" : "failed"}>
                <div className="timeline-head">
                  <strong>
                    {j.ok === true
                      ? `${directionText[String(j.direction)] ?? j.direction} · 信心 ${j.confidence} · ${j.horizon_hours} 小时`
                      : `研判失败 · ${text(j.reason) ?? ""}`}
                  </strong>
                  <span className="muted">{t(j.at)}</span>
                </div>
                {j.ok === true ? (
                  <>
                    <p>{text(j.thesis)}</p>
                    {text(j.counter_points) && (
                      <p className="muted">反面：{text(j.counter_points)}</p>
                    )}
                    {text(j.invalidation) && (
                      <p className="muted">失效条件：{text(j.invalidation)}</p>
                    )}
                    {Array.isArray(j.key_items) && j.key_items.length > 0 && (
                      <details>
                        <summary>引用新闻 {j.key_items.length} 条</summary>
                        <ul>
                          {j.key_items.map(record).map((n) => (
                            <li key={String(n.item_id)}>
                              {text(n.title) ?? `（标题不可显示）${n.item_id}`}
                              <span className="muted">
                                {" "}
                                · {text(n.source) ?? "未知来源"} · {t(n.published_at)}
                              </span>
                            </li>
                          ))}
                        </ul>
                      </details>
                    )}
                    <p className="metadata">
                      <span>
                        {text(j.provider)} · {text(j.model)} · 输入 {String(j.input_count)} 条
                      </span>
                    </p>
                  </>
                ) : (
                  <p className="error">
                    {Array.isArray(j.error_codes) ? j.error_codes.join("；") : ""}
                  </p>
                )}
              </li>
            ))}
          </ol>
        </section>
        <section className="panel" aria-label="动作时间线">
          <div className="panel-head">
            <div>
              <p className="eyebrow">dynamic.actions</p>
              <h2>动作时间线（72 小时）</h2>
            </div>
            <StateNote panel={actionsPanel} />
          </div>
          {actions.length === 0 && <p className="empty">未记录动作。</p>}
          <ol className="timeline">
            {actions.map((a) => (
              <li key={String(a.action_id)}>
                <div className="timeline-head">
                  <strong>
                    {actionText[String(a.kind)] ?? a.kind}
                    {text(a.direction) ? ` · ${directionText[String(a.direction)] ?? a.direction}` : ""}
                  </strong>
                  <span className="muted">{t(a.at)}</span>
                </div>
                <p className="metadata">
                  <span>{a.source === "INJECTION" ? "研判注入" : "仓位管理"}</span>
                  {text(a.reason) && <span>{reasonText[String(a.reason)] ?? a.reason}</span>}
                  {text(a.entry) && <span>入场 {text(a.entry)}</span>}
                  {text(a.close) && <span>收盘 {text(a.close)}</span>}
                  {text(a.close_stop) && <span>收盘止损 {text(a.close_stop)}</span>}
                  {text(a.best) && <span>最有利 {text(a.best)}</span>}
                  {text(a.value) && <span>数值 {text(a.value)}</span>}
                  {text(a.chain_id) && <span>{text(a.chain_id)}</span>}
                </p>
              </li>
            ))}
          </ol>
        </section>
      </div>

      <div className="dynamic-columns">
        <section className="panel" aria-label="行情基线与开仓计划">
          <div className="panel-head">
            <h2>行情基线与下次开仓计划</h2>
          </div>
          <dl className="key-values">
            <div>
              <dt>基线</dt>
              <dd>
                {text(baseline.phase) ?? "NONE"} · {directionText[String(baseline.direction)] ?? ""} ·
                数值 {text(baseline.value) ?? "0"}
              </dd>
            </div>
            {status.next_entry ? (
              <>
                <div>
                  <dt>初始止损距离</dt>
                  <dd>
                    {text(record(status.next_entry).distance)}（收盘{" "}
                    {text(record(status.next_entry).close_distance)}）
                  </dd>
                </div>
                <div>
                  <dt>风险比例 · 权益</dt>
                  <dd>
                    {fmt(num(record(status.next_entry).risk_fraction) !== null ? num(record(status.next_entry).risk_fraction)! * 100 : null, 3)}% ·{" "}
                    {text(record(status.next_entry).equity)}
                  </dd>
                </div>
                <div>
                  <dt>计算时间</dt>
                  <dd>{t(record(status.next_entry).planned_at)}</dd>
                </div>
              </>
            ) : (
              <div>
                <dt>下次开仓计划</dt>
                <dd>未记录</dd>
              </div>
            )}
            <div>
              <dt>正常波动表</dt>
              <dd>
                {text(record(status.noise ?? null).noise_id) ?? "未记录"} ·{" "}
                {t(record(status.noise ?? null).generated_at)}
              </dd>
            </div>
            <div>
              <dt>E15 假设账本</dt>
              <dd>未实现</dd>
            </div>
          </dl>
        </section>
        <section className="panel" aria-label="新闻来源">
          <div className="panel-head">
            <h2>新闻来源</h2>
          </div>
          <ul className="sources">
            {(Array.isArray(status.sources) ? status.sources : []).map(record).map((s) => (
              <li key={String(s.source_id)}>
                <span className={`badge ${s.failing === true ? "state-UNAVAILABLE" : "state-AVAILABLE"}`}>
                  {s.failing === true ? "中断" : "正常"}
                </span>{" "}
                {text(s.source_id)} <span className="muted">· 最近成功 {t(s.last_success)}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}

const actionText: Record<string, string> = {
  INJECT: "注入研判",
  INVALIDATE: "研判失效",
  REVISE: "修订研判",
  OPEN: "开仓",
  TRAIL: "移动止损",
  CLOSE_EXIT: "收盘止损退出",
  TRANSFER: "转入基线",
  CLOSE: "批次结束",
  STOP_SUSPEND: "基线暂停（止损后）",
  BASELINE_RECONFIRM: "基线重开",
  BASELINE_RENEW: "基线续期",
  BASELINE_CLEARED: "基线归零",
};
const reasonText: Record<string, string> = {
  FIRST_DIRECTION: "首次方向",
  NEW_ITEMS: "新编号新闻",
  DIRECTION_FLIP: "方向改变",
  CONFIDENCE_DOWN: "信心下降",
};
