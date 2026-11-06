import { lazy, Suspense, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, Card, Col, Collapse, Empty, Progress, Row, Segmented, Space, Statistic, Table, Tag, Timeline, Tooltip, Typography } from "antd";
import { record, type Json, type Selection } from "../api/client";
import { aggregateCandles, historyRange, RANGES, readHistory, readTimelines, type RangeKey } from "./candle-history";
import { poolView, type Segment } from "./confidence";
import { shortTime, useDisplay } from "./display-settings";
import { useLiveMarket } from "./live-market";
import type { PriceLine, TimeMarker } from "./market-chart";
import {
  actionText,
  chartMarkers,
  quietMarkers,
  tradeMarkers,
  currentProtection,
  directionText,
  dynamicRange,
  judgmentEffects,
  ladderInfo,
  num,
  reasonText,
  text,
  effectText,
} from "./market-model";
import { buildTrades, type Trade } from "./trades";
import { useSession } from "./session";
import { useTheme } from "./theme";
import { listOf, objectOf, panelOf, useProtections, useView } from "./use-view";
const MarketChart = lazy(() => import("./market-chart").then((m) => ({ default: m.MarketChart })));

const UP = "#2e8b57";
const DOWN = "#c4553f";
const signColor = (n: number | null) => (n === null || n === 0 ? undefined : n > 0 ? UP : DOWN);
const money = (n: number | null, digits = 2) => (n === null ? "—" : `${n >= 0 ? "" : "-"}${Math.abs(n).toFixed(digits)}`);
const signed = (n: number | null, digits = 2) => (n === null ? "—" : `${n > 0 ? "+" : n < 0 ? "-" : ""}${Math.abs(n).toFixed(digits)}`);
const amount = (n: number) => String(Number(n.toFixed(6)));
const ratio = (n: number | null) => (n === null ? "—" : `${n > 0 ? "+" : n < 0 ? "-" : ""}${Math.abs(n * 100).toFixed(2)}%`);

// Panels this page draws; others in the same views do not raise the notice.
const USED = new Set(["dynamic.status", "dynamic.judgments", "dynamic.actions", "account", "positions", "candles", "pool", "fills"]);

export function MarketPage() {
  const { selection, capabilities } = useSession();
  const s = selection as Selection;
  const { zone } = useDisplay();
  const { mode } = useTheme();
  const t = (v: Json) => (typeof v === "string" ? shortTime(v, zone) : "—");
  const dynamic = useView("dynamic", () => dynamicRange());
  const sentiment = useView("sentiment");
  const execution = useView("execution", (): Record<string, string> => (capabilities ? { limit: String(capabilities.max_limit) } : {}));
  const live = useLiveMarket(s.selection_id);

  // S4-021: 24 hours comes with the page; longer ranges read 5-minute bars.
  const [rangeKey, setRangeKey] = useState<RangeKey>("24h");
  const maxLimit = capabilities?.max_limit;
  const longRange = rangeKey !== "24h" && !!maxLimit;
  const history = useQuery({
    queryKey: ["candle-history", s.selection_id, s.binding_version, rangeKey],
    enabled: longRange,
    staleTime: 5 * 60_000,
    refetchInterval: 5 * 60_000,
    retry: 1,
    queryFn: ({ signal }) => {
      const r = historyRange(rangeKey);
      return readHistory(s, r.start, r.end, maxLimit as number, signal).then((v) => ({ ...v, minutes: r.minutes }));
    },
  });
  const marks = useQuery({
    queryKey: ["chart-marks", s.selection_id, s.binding_version, rangeKey],
    enabled: longRange,
    staleTime: 5 * 60_000,
    refetchInterval: 5 * 60_000,
    retry: 1,
    queryFn: ({ signal }) => {
      const r = historyRange(rangeKey);
      return readTimelines(s, r.start, r.end, maxLimit as number, signal);
    },
  });

  const panels = dynamic.panels;
  const status = objectOf(panelOf(panels, "dynamic.status"));
  const batch = status.batch ? record(status.batch) : null;
  const chain = status.chain ? record(status.chain) : null;
  const account = objectOf(panelOf(panels, "account"));
  const position = listOf(panelOf(panels, "positions")).find(
    (p) => record(p.instrument_key).instrument_id === s.instrument_key.instrument_id && p.owner_id === s.owner_id,
  );
  const protections = useProtections(listOf(panelOf(panels, "protections")));
  const judgments = listOf(panelOf(panels, "dynamic.judgments"));
  const actions = listOf(panelOf(panels, "dynamic.actions"));
  const dayCandles = listOf(panelOf(panels, "candles"));
  const minutes = history.data?.minutes ?? 5;
  const candles = useMemo(
    () => (longRange ? aggregateCandles(history.data?.bars ?? [], minutes) : dayCandles),
    [longRange, history.data, minutes, dayCandles],
  );
  const lastCandle = candles
    .map(record)
    .filter((c) => c.final === true && typeof c.close_at === "string")
    .sort((a, b) => String(a.close_at).localeCompare(String(b.close_at)))
    .at(-1);
  const lastClose = lastCandle ? num(lastCandle.close) : null;
  const refMark = live.state === "LIVE" && live.mark ? num(live.mark.price) : null;
  const mark = refMark ?? lastClose;

  // Stop and price levels drawn on the chart and listed beside it.
  const strategy = currentProtection(protections, "STRATEGY");
  const insurance = currentProtection(protections, "INSURANCE");
  const ladder = ladderInfo(batch);
  const levels: (PriceLine & { note: string })[] = [];
  if (insurance) levels.push({ title: "极端行情保护单", price: num(record(insurance.plan).trigger_price) ?? NaN, color: "#7a2b22", dashed: true, note: "交易所条件单" });
  if (strategy) levels.push({ title: "交易所止损单", price: num(record(strategy.plan).trigger_price) ?? NaN, color: "#c4553f", note: "交易所条件单" });
  if (batch) {
    levels.push({ title: "收盘止损", price: num(batch.close_stop) ?? NaN, color: "#d08a1d", dashed: true, note: `5 分钟收盘越过即平仓 · ${ladder?.source ?? ""}` });
    if (ladder && ladder.step !== null && ladder.nextBest !== null)
      levels.push({ title: `阶梯第 ${ladder.rung + 1} 级`, price: ladder.nextBest, color: "#7b5cc4", dashed: true, note: `收盘到达后止损移到 ${ladder.nextStop?.toFixed(2)}` });
    if (batch.trailing !== true) levels.push({ title: "移动止损启动价", price: num(batch.trail_trigger) ?? NaN, color: UP, dashed: true, note: "到达后开始跟随" });
  }
  // The trading layer's own average entry, there from the first fill on.
  if (position && num(position.quantity))
    levels.push({ title: "持仓均价", price: num(position.average_entry) ?? NaN, color: "#6b756f", note: "交易层持仓" });
  const valid = levels.filter((l) => Number.isFinite(l.price));
  const lines: PriceLine[] = useMemo(() => valid.map(({ note: _, ...l }) => l), [JSON.stringify(valid.map((l) => [l.title, l.price]))]);
  const markJudgments = longRange && marks.data ? marks.data.judgments.map(record) : judgments;
  const markActions = longRange && marks.data ? marks.data.actions.map(record) : actions;
  const pool = poolView(objectOf(panelOf(sentiment.panels, "pool")));
  const fills = listOf(panelOf(execution.panels, "fills"));
  const trades = useMemo(() => buildTrades(fills, mark), [JSON.stringify(fills.map((f) => f.external_fill_id ?? f.happened_at)), mark]);
  // Opens and closes come from the fills, so a new position shows at once.
  const markers: TimeMarker[] = useMemo(
    () =>
      quietMarkers([
        ...chartMarkers(markJudgments, markActions.filter((a) => a.source !== "POSITION")),
        ...tradeMarkers(trades),
      ]),
    [
      JSON.stringify(markJudgments.map((j) => [j.judgment_id, j.direction])),
      JSON.stringify(markActions.map((a) => a.action_id)),
      JSON.stringify(trades.map((x) => [x.openedAt, x.closedAt])),
    ],
  );

  const equity = num(account.equity);
  const realized = num(account.realized_pnl);
  const unrealized = num(account.unrealized_pnl);
  const costs = (num(account.fees) ?? 0) + (num(account.funding) ?? 0);
  const total = num(record(record(account.pnl).total).trading ?? null);
  const base = equity !== null && total !== null ? equity - total : null;
  const closed = trades.filter((x) => x.closedAt !== null);
  const wins = closed.filter((x) => x.realized > 0).length;
  const qty = position ? num(position.quantity) : null;
  const avg = position ? num(position.average_entry) : null;

  return (
    <div className="page">
      {dynamic.error && <Alert type="warning" showIcon message="本次刷新失败，显示的是上一次成功读取的数据" />}
      {!dynamic.error && [...dynamic.panels, ...sentiment.panels, ...execution.panels].some((p) => USED.has(p.source) && (p.state === "STALE" || p.state === "UNAVAILABLE")) && (
        <Alert type="warning" showIcon message="部分数据暂时读不到，显示的是上一次读取的结果" />
      )}
      <Row gutter={[12, 12]} className="summary">
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic title="权益（USDT）" value={money(equity)} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic
              title="累计盈亏"
              value={signed(total)}
              valueStyle={{ color: signColor(total) }}
              suffix={base && total !== null ? <small>{ratio(total / base)}</small> : undefined}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic title="已实现盈亏" value={signed(realized)} valueStyle={{ color: signColor(realized) }} />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic
              title="浮动盈亏"
              value={signed(unrealized)}
              valueStyle={{ color: signColor(unrealized) }}
              suffix={qty && avg && unrealized !== null ? <small>{ratio(unrealized / Math.abs(qty * avg))}</small> : undefined}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic
              title="当前持仓"
              value={qty ? `${qty < 0 ? "空" : "多"} ${Math.abs(qty)}` : "空仓"}
              valueStyle={{ color: qty ? (qty < 0 ? DOWN : UP) : undefined }}
              suffix={qty && avg ? <small>@ {avg.toFixed(2)}</small> : undefined}
            />
          </Card>
        </Col>
        <Col xs={12} md={8} xl={4}>
          <Card size="small">
            <Statistic
              title="已平仓交易"
              value={closed.length}
              suffix={<small>胜 {wins} · 费用 {costs.toFixed(2)}</small>}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={[12, 12]}>
        <Col xs={24} xl={17}>
          <Card
            size="small"
            title={
              <span>
                {s.instrument_key.instrument_id} · {mark !== null ? mark.toFixed(2) : "—"}{" "}
                <Typography.Text type="secondary" className="small">
                  {refMark !== null ? "实时参考价" : "最新收盘"}
                </Typography.Text>
              </span>
            }
            extra={
              <Segmented
                size="small"
                value={rangeKey}
                onChange={(v) => setRangeKey(v as RangeKey)}
                options={RANGES.map((r) => ({ value: r.key, label: r.label, disabled: r.key !== "24h" && !maxLimit }))}
              />
            }
          >
            {longRange && history.isPending && <Typography.Text type="secondary">正在读取历史 K 线…</Typography.Text>}
            {longRange && history.isError && <Alert type="error" showIcon message="历史 K 线读取失败，请稍后重试" />}
            <Suspense fallback={<Empty description="正在装载图表…" />}>
              <MarketChart key={rangeKey} candles={candles} lines={lines} markers={markers} live={longRange ? null : live.kline} height={460} dark={mode === "dark"} />
            </Suspense>
          </Card>
        </Col>
        <Col xs={24} xl={7}>
          <Space direction="vertical" size={12} style={{ width: "100%" }}>
            <ConfidenceCard chain={chain} pool={pool.net} segments={pool.segments} judgments={judgments} t={t} />
            <Card size="small" title="止损位">
              {valid.length === 0 ? (
                <Typography.Text type="secondary">当前没有持仓，没有止损线</Typography.Text>
              ) : (
                <ul className="levels">
                  {valid
                    .sort((a, b) => b.price - a.price)
                    .map((l) => (
                      <li key={l.title}>
                        <span className="swatch" style={{ background: l.color }} />
                        <span className="level-name">{l.title}</span>
                        <Tooltip title={l.note}>
                          <span className="level-price">{l.price.toFixed(2)}</span>
                        </Tooltip>
                        <span className="level-distance">{mark ? ratio((l.price - mark) / mark) : ""}</span>
                      </li>
                    ))}
                </ul>
              )}
            </Card>
          </Space>
        </Col>
      </Row>

      <Card size="small" title="成交单" className="trades">
        <TradesTable trades={trades} t={t} />
      </Card>

      <Collapse
        className="records"
        items={[
          { key: "judgments", label: `研判记录（72 小时 · ${judgments.length} 条）`, children: <JudgmentList judgments={judgments} actions={actions} t={t} /> },
          { key: "actions", label: `动作记录（72 小时 · ${actions.length} 条）`, children: <ActionList actions={actions} t={t} /> },
        ]}
      />
    </div>
  );
}

const SHORT_SHADES = ["#c4553f", "#d9775f", "#a8432f", "#e3937f", "#8f3a28"];
const LONG_SHADES = ["#2e8b57", "#4fa877", "#23704a", "#74bf95", "#1b5b3b"];
function ConfidenceCard({
  chain,
  pool,
  segments,
  judgments,
  t,
}: {
  chain: Record<string, Json> | null;
  pool: number | null;
  segments: Segment[];
  judgments: Record<string, Json>[];
  t: (v: Json) => string;
}) {
  const conf = chain ? Number(chain.confidence) : 0;
  const dir = chain ? String(chain.direction) : "FLAT";
  const color = dir === "SHORT" ? DOWN : dir === "LONG" ? UP : "#8c8c8c";
  const byId = new Map(judgments.map((j) => [String(j.judgment_id), j]));
  return (
    <Card size="small" title="当前信心">
      <div className="confidence-head">
        <Progress
          type="dashboard"
          size={120}
          percent={(conf / 5) * 100}
          strokeColor={color}
          format={() => (
            <span className="confidence-value">
              <b style={{ color }}>{chain ? conf : "—"}</b>
              <small>/5</small>
            </span>
          )}
        />
        <div>
          <Typography.Title level={4} style={{ color, margin: 0 }}>
            {chain ? (directionText[dir] ?? dir) : "无方向"}
          </Typography.Title>
          <Typography.Text type="secondary">
            {chain ? `连续 ${chain.judgment_count} 次研判 · 起于 ${t(chain.since)}` : "当前没有判断链"}
          </Typography.Text>
          <div>
            <Typography.Text>信心池净值 </Typography.Text>
            <Typography.Text strong style={{ color: signColor(pool) }}>
              {pool === null ? "—" : pool.toFixed(2)}
            </Typography.Text>
          </div>
        </div>
      </div>
      <Typography.Text type="secondary" className="small">
        信心来源（{segments.length} 笔有效研判，按剩余影响量）
      </Typography.Text>
      <div className="source-bar" role="list" aria-label="信心来源">
        {segments.length === 0 && <div className="source-empty">没有有效贡献</div>}
        {segments.map((seg, i) => {
          const j = seg.judgmentId ? byId.get(seg.judgmentId) : undefined;
          const shades = seg.direction < 0 ? SHORT_SHADES : LONG_SHADES;
          return (
            <Tooltip
              key={seg.eventId}
              overlayClassName="source-tip"
              title={
                <div>
                  <b>
                    {seg.direction < 0 ? "偏空" : "偏多"} · 占 {(seg.share * 100).toFixed(1)}% · {t(seg.effectiveAt)}
                  </b>
                  {j ? (
                    <>
                      <p>{text(j.thesis)}</p>
                      {Array.isArray(j.key_items) && j.key_items.length > 0 && (
                        <ul>
                          {j.key_items.map(record).map((n) => (
                            <li key={String(n.item_id)}>{text(n.title) ?? `${text(n.source) ?? "新闻"}（标题不可显示）`}</li>
                          ))}
                        </ul>
                      )}
                    </>
                  ) : (
                    <p>研判 {seg.judgmentId ?? seg.eventId}（已超出 72 小时记录范围）</p>
                  )}
                </div>
              }
            >
              <div role="listitem" className="source-seg" style={{ flexGrow: seg.share, background: shades[i % shades.length] }}>
                {seg.share > 0.12 ? `${(seg.share * 100).toFixed(0)}%` : ""}
              </div>
            </Tooltip>
          );
        })}
      </div>
    </Card>
  );
}

function TradesTable({ trades, t }: { trades: Trade[]; t: (v: Json) => string }) {
  if (trades.length === 0) return <Empty description="还没有成交" />;
  return (
    <Table<Trade>
      size="small"
      rowKey={(r) => r.openedAt}
      dataSource={trades}
      pagination={{ pageSize: 10, hideOnSinglePage: true }}
      scroll={{ x: 980 }}
      rowClassName={(r) => (r.closedAt === null ? "trade-open" : "")}
      columns={[
        {
          title: "方向",
          dataIndex: "direction",
          width: 70,
          render: (d) => <Tag color={d === "SHORT" ? "red" : "green"}>{d === "SHORT" ? "空" : "多"}</Tag>,
        },
        { title: "开仓时间", dataIndex: "openedAt", width: 150, render: (v) => t(v) },
        {
          title: "平仓时间",
          dataIndex: "closedAt",
          width: 150,
          render: (v) => (v ? t(v) : <Tag color="processing">持仓中</Tag>),
        },
        { title: "成交量", dataIndex: "openQuantity", align: "right", render: (v: number, r) => (r.closedAt ? amount(v) : `${amount(r.remaining)} / ${amount(v)}`) },
        { title: "开仓均价", dataIndex: "averageEntry", align: "right", render: (v: number) => v.toFixed(2) },
        { title: "开仓成交额", dataIndex: "openNotional", align: "right", render: (v: number) => v.toFixed(2) },
        { title: "平仓均价", dataIndex: "averageExit", align: "right", render: (v: number | null) => (v === null ? "—" : v.toFixed(2)) },
        { title: "平仓成交额", dataIndex: "closeNotional", align: "right", render: (v: number, r) => (r.closeQuantity > 0 ? v.toFixed(2) : "—") },
        {
          title: "盈亏",
          align: "right",
          render: (_, r) => {
            const v = r.closedAt ? r.realized : r.unrealized;
            return (
              <Tooltip title={r.closedAt ? "已实现（价格盈亏，不含手续费）" : `浮动盈亏${r.closeQuantity > 0 ? `；已部分平仓，已实现 ${signed(r.realized)}` : ""}`}>
                <span style={{ color: signColor(v) }}>
                  {signed(v)}
                  {!r.closedAt && <small className="muted"> 浮</small>}
                </span>
              </Tooltip>
            );
          },
        },
        {
          title: "盈亏比例",
          align: "right",
          render: (_, r) => {
            const v = r.closedAt ? r.realizedRatio : r.unrealizedRatio;
            return <span style={{ color: signColor(v) }}>{ratio(v)}</span>;
          },
        },
        { title: "手续费", dataIndex: "fees", align: "right", render: (v: number) => v.toFixed(3) },
      ]}
    />
  );
}

function JudgmentList({ judgments, actions, t }: { judgments: Record<string, Json>[]; actions: Record<string, Json>[]; t: (v: Json) => string }) {
  const effects = judgmentEffects(judgments, actions);
  if (judgments.length === 0) return <Empty description="没有研判" />;
  return (
    <Timeline
      items={[...judgments]
        .sort((a, b) => String(b.at).localeCompare(String(a.at)))
        .map((j) => {
          const e = effects.get(String(j.judgment_id));
          const dir = String(j.direction);
          return {
            key: String(j.judgment_id),
            color: j.ok !== true ? "gray" : dir === "SHORT" ? "red" : dir === "LONG" ? "green" : "blue",
            children: (
              <div className="record">
                <Space wrap>
                  <b>{j.ok === true ? `${directionText[dir] ?? dir} · 信心 ${j.confidence}/5` : "研判失败"}</b>
                  <Typography.Text type="secondary">{t(j.at)}</Typography.Text>
                  {j.ok === true &&
                    (e ? (
                      <Tag color="gold">{e.kinds.map((k) => effectText[k] ?? k).join("·")}{e.reasons.length ? `（${e.reasons.map((r) => reasonText[r] ?? r).join("、")}）` : ""}</Tag>
                    ) : (
                      <Tag>仅记录</Tag>
                    ))}
                </Space>
                {j.ok === true ? <p>{text(j.thesis)}</p> : <p className="muted">{text(j.reason)}</p>}
                {Array.isArray(j.key_items) && j.key_items.length > 0 && (
                  <details>
                    <summary>引用新闻 {j.key_items.length} 条</summary>
                    <ul>
                      {j.key_items.map(record).map((n) => (
                        <li key={String(n.item_id)}>
                          {text(n.title) ?? "（标题不可显示）"} <span className="muted">· {text(n.source)} · {t(n.published_at)}</span>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </div>
            ),
          };
        })}
    />
  );
}

function ActionList({ actions, t }: { actions: Record<string, Json>[]; t: (v: Json) => string }) {
  if (actions.length === 0) return <Empty description="没有动作" />;
  return (
    <Timeline
      items={[...actions]
        .sort((a, b) => String(b.at).localeCompare(String(a.at)))
        .map((a) => ({
          key: String(a.action_id),
          color: a.source === "POSITION" ? "blue" : "gray",
          children: (
            <Space wrap>
              <b>{actionText[String(a.kind)] ?? a.kind}</b>
              <Typography.Text type="secondary">{t(a.at)}</Typography.Text>
              {text(a.reason) && <Tag>{reasonText[String(a.reason)] ?? a.reason}</Tag>}
              {num(a.entry) !== null && <span>开仓 {num(a.entry)!.toFixed(2)}</span>}
              {num(a.close) !== null && <span>收盘 {num(a.close)!.toFixed(2)}</span>}
              {num(a.close_stop) !== null && <span>收盘止损 {num(a.close_stop)!.toFixed(2)}</span>}
            </Space>
          ),
        }))}
    />
  );
}
