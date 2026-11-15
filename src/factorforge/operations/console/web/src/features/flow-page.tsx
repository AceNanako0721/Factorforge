import { useEffect, useMemo, useState } from "react";
import { Alert, Button, Descriptions, Empty, Space, Table, Tag, Tooltip, Typography } from "antd";
import { record, type Json, type Selection } from "../api/client";
import { BlockDiagram, FunctionPage, formulaHtml } from "./block-diagram";
import { poolView } from "./confidence";
import { shortTime, useDisplay } from "./display-settings";
import { flowNodes, type FlowNode, type Health } from "./flow-model";
import { directionText, dynamicRange, text } from "./market-model";
import { useReplay } from "./replay-data";
import { buildOrders, ctxAt, startFrame, type Ctx, type Order } from "./replay-model";
import { GO, LEAF, SYS, dirName, fx, hm, jName, titleOf, type Block } from "./replay-systems";
import { ReplayTimeline } from "./replay-timeline";
import { useSession } from "./session";
import { listOf, objectOf, panelOf, useProtections, useView } from "./use-view";

const healthColor: Record<Health, string> = { ok: "#2e8b57", warn: "#d08a1d", error: "#c4553f", idle: "#8c8c8c", loading: "#bfbfbf" };
const healthText: Record<Health, string> = { ok: "正常", warn: "注意", error: "异常", idle: "未启用", loading: "读取中" };
const exitText: Record<string, string> = { TARGET: "目标归零", CLOSE_STOP: "收盘止损", VENUE_STOP: "交易所止损", UNFILLED: "未成交" };

// Panels this page draws; others in the same views do not raise the notice.
const USED = new Set(["dynamic.status", "dynamic.judgments", "account", "positions", "run", "object", "pool", "decisions", "trading.operations"]);

// S4-022: the live pipeline drawn as nested block diagrams. The top level is
// the pipeline with each module's live state; a module opens its own diagram,
// down to function pages. Below, the timeline and the orders (S4-024) pick
// the round every level shows.
export function FlowPage() {
  const { selection } = useSession();
  const s = selection as Selection;
  const { zone } = useDisplay();
  const t = (v: Json) => (typeof v === "string" ? shortTime(v, zone) : "—");
  const dynamic = useView("dynamic", () => dynamicRange());
  const overview = useView("overview");
  const operations = useView("operations");
  const learning = useView("learning");
  const batches = useView("replay", () => ({ source: "batches", from: new Date(Date.now() - 86_400_000).toISOString().replace(/\.\d{3}Z$/, "Z") }));

  const status = objectOf(panelOf(dynamic.panels, "dynamic.status"));
  const decisions = listOf(panelOf(overview.panels, "decisions")).sort((a, b) => String(b.available_cutoff).localeCompare(String(a.available_cutoff)));
  const pool = poolView(objectOf(panelOf(overview.panels, "pool")));
  const parameters = listOf(panelOf(learning.panels, "parameters"));
  const object = objectOf(panelOf(overview.panels, "object"));
  const judgments = listOf(panelOf(dynamic.panels, "dynamic.judgments"));
  const protections = useProtections(listOf(panelOf(dynamic.panels, "protections")));
  // Until the first read lands, no module is judged: an empty answer would
  // otherwise read as "not enabled" or "flat".
  const loading = dynamic.pending || overview.pending || operations.pending;
  const judged = flowNodes({
    now: Date.now(),
    status,
    judgments,
    account: objectOf(panelOf(dynamic.panels, "account")),
    position: listOf(panelOf(dynamic.panels, "positions")).find((p) => record(p.instrument_key).instrument_id === s.instrument_key.instrument_id && p.owner_id === s.owner_id),
    protections,
    object,
    run: objectOf(panelOf(overview.panels, "run")),
    operations: objectOf(panelOf(operations.panels, "trading.operations")),
    decision: decisions[0],
    pool: { net: pool.net, segments: pool.segments.length },
    parameters: parameters.find((p) => p.version === object.parameter_version) ?? parameters[0],
    time: t,
  });
  const nodes = loading ? judged.map((n) => ({ ...n, health: "loading" as const, headline: "正在读取…", rows: [], params: [] })) : judged;
  const live = new Map(nodes.map((n) => [n.id, n]));
  const problems = nodes.filter((n) => n.health === "error" || n.health === "warn");

  const orders = useMemo(() => buildOrders(listOf(panelOf(batches.panels, "batches"))), [batches.panels]);
  const [orderId, setOrderId] = useState<string | null>(null);
  const order = orders.find((o) => o.batchId === orderId) ?? orders.find((o) => !o.closed) ?? null;
  const replay = useReplay(order);
  const data = replay.data && replay.data.order.batchId === order?.batchId ? replay.data : undefined;
  const [index, setIndex] = useState(0);
  const [follow, setFollow] = useState(true);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(2);
  const [path, setPath] = useState<string[]>(["root"]);
  const [selected, setSelected] = useState<string | null>(null);
  const [showValues, setShowValues] = useState(true);

  // A closed order plays once from the decision that opened it; the open one
  // follows the latest round until the user scrubs away from it.
  const loadedKey = data ? `${data.order.batchId}:${data.frames.length}` : "";
  const [shownOrder, setShownOrder] = useState("");
  useEffect(() => {
    if (!data || data.frames.length === 0) return;
    if (shownOrder !== data.order.batchId) {
      setShownOrder(data.order.batchId);
      if (data.order.closed) {
        setFollow(false);
        setIndex(startFrame(data));
        setPlaying(true);
      } else {
        setFollow(true);
        setPlaying(false);
        setIndex(data.frames.length - 1);
      }
    } else if (follow) setIndex(data.frames.length - 1);
  }, [loadedKey]);
  useEffect(() => {
    if (!playing || !data) return;
    const last = data.frames.length - 1;
    const id = setInterval(() => {
      setIndex((i) => {
        if (i >= last) {
          setPlaying(false);
          return i;
        }
        return i + 1;
      });
    }, 700 / speed);
    return () => clearInterval(id);
  }, [playing, speed, data]);

  const at = data ? Math.min(index, data.frames.length - 1) : -1;
  const isLive = !!data && !data.order.closed && at === data.frames.length - 1;
  const ctx: Ctx | null = data && at >= 0 ? ctxAt(data, at, orders, isLive) : null;
  const prev: Ctx | null = data && at > 0 ? ctxAt(data, at - 1, orders, false) : null;
  const key = path[path.length - 1];
  const leaf = key.startsWith("leaf:") ? key.slice(5) : null;
  const enter = (b: Block) => {
    if (b.leaf) setPath((p) => [...p, "leaf:" + b.leaf]);
    else if (b.sub) setPath((p) => [...p, b.sub as string]);
    else return;
    setSelected(null);
  };
  const go = (i: number) => {
    setPath((p) => p.slice(0, i + 1));
    setSelected(null);
  };

  return (
    <div className="page">
      <Space className="flow-summary" wrap>
        {loading ? (
          <Typography.Text type="secondary">正在读取各模块状态…</Typography.Text>
        ) : problems.length === 0 ? (
          <Alert type="success" showIcon message="所有模块运行正常" />
        ) : (
          <Alert type={problems.some((p) => p.health === "error") ? "error" : "warning"} showIcon message={problems.map((p) => `${p.title}：${p.headline}`).join("；")} />
        )}
        {(dynamic.error || overview.error) && <Alert type="warning" showIcon message="本次刷新失败，显示的是上一次成功读取的数据" />}
        {!dynamic.error && [...dynamic.panels, ...overview.panels, ...operations.panels].some((p) => USED.has(p.source) && (p.state === "STALE" || p.state === "UNAVAILABLE")) && (
          <Alert type="warning" showIcon message="部分数据暂时读不到，显示的是上一次读取的结果" />
        )}
      </Space>
      <section className="rp-model">
        <div className="rp-canvas">
          <div className="rp-bar">
            <nav className="rp-crumbs" aria-label="层级">
              {path.map((k, i) => (
                <span key={i}>
                  {i > 0 && <span className="rp-sep">›</span>}
                  <button disabled={i === path.length - 1} onClick={() => go(i)}>
                    {i === 0 ? "全部模块" : titleOf(k)}
                  </button>
                </span>
              ))}
            </nav>
            <span className={`rp-mode${isLive ? " live" : data ? " replay" : ""}`}>
              <span className="dot" />
              {!data ? (order ? "正在读取订单记录…" : "没有订单") : isLive ? "实时（持仓中）" : data.order.closed ? "回放 · 已平仓订单" : "回放 · 持仓中订单的过去"}
            </span>
            <button className="bd-btn" disabled={path.length === 1} onClick={() => go(path.length - 2)}>上一层</button>
            <button className={`bd-btn${showValues ? " on" : ""}`} onClick={() => setShowValues((v) => !v)}>数值</button>
          </div>
          {leaf ? (
            <FunctionPage leaf={leaf} ctx={ctx} />
          ) : (
            <BlockDiagram system={key} ctx={ctx} prev={prev} selected={selected} onSelect={setSelected} onEnter={enter} showValues={showValues} flowing={playing || isLive || !data} live={live} />
          )}
          <div className="rp-legend">单击模块看它这一轮的输入、处理和输出；双击进入下一层，最底层是公式。虚线方框是 From 标签，代替会交叉的长连线。</div>
        </div>
        <Inspector path={path} selected={selected} ctx={ctx} live={live} judgments={judgments} t={t} onEnter={enter} />
      </section>
      {replay.error && <Alert type="warning" showIcon message="这笔订单的回放记录读取失败，稍后会重试" />}
      {data?.truncated && <Alert type="info" showIcon message="这笔订单的记录太多，只读取了前面的部分" />}
      {data && data.frames.length > 0 && (
        <ReplayTimeline
          replay={data}
          index={at}
          onSeek={(i) => {
            setFollow(false);
            setIndex(i);
          }}
          playing={playing}
          onPlay={(on) => {
            if (on && at >= data.frames.length - 1) setIndex(0);
            if (on) setFollow(false);
            setPlaying(on);
          }}
          speed={speed}
          onSpeed={setSpeed}
          onLive={() => {
            setPlaying(false);
            setFollow(true);
            setIndex(data.frames.length - 1);
          }}
          live={isLive}
        />
      )}
      <OrdersTable orders={orders} current={order} onSelect={(o) => setOrderId(o.batchId)} />
    </div>
  );
}

type InspectorProps = { path: string[]; selected: string | null; ctx: Ctx | null; live: Map<string, FlowNode>; judgments: Record<string, Json>[]; t: (v: Json) => string; onEnter: (b: Block) => void };
function Inspector({ path, selected, ctx, live, judgments, t, onEnter }: InspectorProps) {
  const key = path[path.length - 1];
  if (key.startsWith("leaf:")) {
    const L = LEAF[key.slice(5)];
    return (
      <aside className="rp-insp">
        <Typography.Title level={5}>{L?.title}</Typography.Title>
        <Typography.Text type="secondary">函数 · 最底层</Typography.Text>
        <div className="rp-h">这一步在做什么</div>
        <p>{L?.ex}</p>
        {L?.src && <div className="rp-go">{GO[L.src]}</div>}
        <FrameSummary ctx={ctx} />
      </aside>
    );
  }
  const sys = SYS[key];
  const b = sys.blocks.find((x) => x.id === selected);
  if (!b) {
    return (
      <aside className="rp-insp">
        <Typography.Title level={5}>{key === "root" ? "全部模块" : sys.title}</Typography.Title>
        <Typography.Text type="secondary">点一个模块看它的输入、处理和输出；双击进入下一层</Typography.Text>
        <FrameSummary ctx={ctx} />
        {key === "root" && (
          <>
            <div className="rp-h">开仓数量怎么来的</div>
            <p>止损与阶梯先算出这一批愿意亏的比例 f 和止损距离 D，发给策略决策；策略决策在开仓那一轮用 权益 × f 得到预算，再除以每张可能亏的钱，得到单位数量 Q 并冻结；之后每一轮按档位比例取 Q 的一部分作为目标数量。</p>
          </>
        )}
      </aside>
    );
  }
  const node = key === "root" && b.live ? live.get(b.live) : undefined;
  const value = (f: ((c: Ctx) => string) | undefined) => {
    if (!f || !ctx) return "—";
    try {
      return f(ctx);
    } catch {
      return "—";
    }
  };
  const ins = sys.wires.filter((w) => w.b.startsWith(b.id + "."));
  const outs = sys.wires.filter((w, i, a) => w.a.startsWith(b.id + ".") && a.findIndex((x) => x.a === w.a) === i);
  const L = b.leaf ? LEAF[b.leaf] : undefined;
  const kind = { sub: "子系统", fn: "函数", in: "输入端口", out: "输出端口", const: "登记值", from: "From 标签" }[b.k];
  return (
    <aside className="rp-insp">
      <Typography.Title level={5}>{node?.title ?? b.t}</Typography.Title>
      <Typography.Text type="secondary">
        {kind}
        {b.st ? " · " + b.st : ""}
      </Typography.Text>
      {node && (!ctx || ctx.live) && (
        <>
          <div className="rp-h">
            实时状态 <Tag color={healthColor[node.health]}>{healthText[node.health]}</Tag>
          </div>
          <Typography.Paragraph strong>{node.headline}</Typography.Paragraph>
          {node.rows.length > 0 ? (
            <Descriptions size="small" column={1} bordered items={node.rows.map((r, i) => ({ key: i, label: r.label, children: r.value }))} />
          ) : (
            <Empty description="没有更多状态" image={Empty.PRESENTED_IMAGE_SIMPLE} />
          )}
          {node.id === "judgment" && <RecentJudgments judgments={judgments} t={t} />}
          {node.params.length > 0 && (
            <div>
              <Space className="params-head">
                <span className="rp-h">参数</span>
                <Tooltip title="管理台只读。参数修改需要经过策略参数发布流程，暂不在这里修改。">
                  <Button size="small" disabled>
                    修改
                  </Button>
                </Tooltip>
              </Space>
              <Table
                size="small"
                pagination={false}
                rowKey="name"
                dataSource={node.params}
                columns={[
                  { title: "参数", dataIndex: "name" },
                  { title: "当前值", dataIndex: "value", align: "right" },
                  { title: "说明", dataIndex: "note", render: (v) => <Typography.Text type="secondary">{v}</Typography.Text> },
                ]}
              />
            </div>
          )}
        </>
      )}
      {b.k === "from" && <p>From 标签：等同于从「{b.src}」拉过来的一条线，用来避免长连线交叉。</p>}
      {b.k === "const" && <p>策略框架登记的值，对这个对象的每一批都一样。</p>}
      {ins.length > 0 && (
        <>
          <div className="rp-h">INPUT</div>
          <dl className="rp-kv">
            {ins.map((w, i) => (
              <div key={i}>
                <dt>{w.l ?? w.a.split(".")[0]}</dt>
                <dd>{value(w.v)}</dd>
              </div>
            ))}
          </dl>
        </>
      )}
      {L && ctx && (
        <>
          <div className="rp-h">处理</div>
          <div className="rp-formula" dangerouslySetInnerHTML={{ __html: formulaHtml(L.sym + "<br>" + safeNum(L, ctx)) }} />
          <p>{L.ex}</p>
        </>
      )}
      {b.sub && !L && (
        <>
          <div className="rp-h">处理</div>
          <p>
            由这些步骤组成：
            {SYS[b.sub].blocks
              .filter((x) => x.k === "fn" || x.k === "sub")
              .map((x) => x.t)
              .join("、")}
            。
          </p>
        </>
      )}
      {(outs.length > 0 || b.v) && (
        <>
          <div className="rp-h">OUTPUT</div>
          <dl className="rp-kv">
            {outs.length > 0 ? (
              outs.map((w, i) => (
                <div key={i}>
                  <dt>{w.l ?? "输出"}</dt>
                  <dd>{value(w.v)}</dd>
                </div>
              ))
            ) : (
              <div>
                <dt>值</dt>
                <dd>{value(b.v)}</dd>
              </div>
            )}
          </dl>
        </>
      )}
      {(b.sub || b.leaf) && (
        <Button size="small" onClick={() => onEnter(b)}>
          进入 {node?.title ?? b.t} ›
        </Button>
      )}
    </aside>
  );
}
function safeNum(L: { num: (c: Ctx) => string }, ctx: Ctx) {
  try {
    return L.num(ctx);
  } catch {
    return "这一轮缺少计算所需的记录";
  }
}

function FrameSummary({ ctx }: { ctx: Ctx | null }) {
  if (!ctx) return null;
  const f = ctx.f;
  return (
    <>
      <div className="rp-h">这一轮（{hm(f.t)} UTC）</div>
      <dl className="rp-kv">
        <div>
          <dt>研判</dt>
          <dd>{f.j ? `${jName(f.j.dir)}/${f.j.conf} ${f.j.h}h` : "—"}</dd>
        </div>
        <div>
          <dt>合成 X / 档位</dt>
          <dd>
            {fx(f.cb, 3)} / {f.lv}
          </dd>
        </div>
        <div>
          <dt>目标 / 实际</dt>
          <dd>
            {fx(f.tg, 2)} / {fx(f.ac, 2)}
          </dd>
        </div>
        <div>
          <dt>价格 / 权益</dt>
          <dd>
            {fx(f.px, 2)} / {fx(f.eq, 2)}
          </dd>
        </div>
        <div>
          <dt>原因码</dt>
          <dd>
            {f.rc.map((r) => (
              <Tag key={r}>{r}</Tag>
            ))}
          </dd>
        </div>
      </dl>
    </>
  );
}

function OrdersTable({ orders, current, onSelect }: { orders: Order[]; current: Order | null; onSelect: (o: Order) => void }) {
  return (
    <section className="rp-orders">
      <div className="rp-orders-head">
        <Typography.Title level={5} style={{ margin: 0 }}>
          订单（从开仓到全部平仓为一笔）
        </Typography.Title>
        <Typography.Text type="secondary">点一行载入该订单：已平仓的从开仓决策播放到平仓，持仓中的显示当前状态</Typography.Text>
      </div>
      <Table
        size="small"
        rowKey="batchId"
        dataSource={orders}
        pagination={{ pageSize: 20, hideOnSinglePage: true }}
        scroll={{ x: 760 }}
        rowClassName={(o) => (o.batchId === current?.batchId ? "rp-row sel" : "rp-row")}
        onRow={(o) => ({ onClick: () => onSelect(o), onKeyDown: (e) => e.key === "Enter" && onSelect(o), tabIndex: 0 })}
        columns={[
          { title: "订单", dataIndex: "id", render: (v) => <span className="mono">{v}</span> },
          { title: "方向", dataIndex: "dir", render: (v: number) => <span className={v > 0 ? "pos" : "neg"}>{dirName(v)}</span> },
          { title: "开仓（UTC）", dataIndex: "opened", render: (v: string) => <span className="mono">{hm(v)}</span> },
          { title: "平仓（UTC）", dataIndex: "closed", render: (v: string | null) => (v ? <span className="mono">{hm(v)}</span> : <Tag color="green">持仓中</Tag>) },
          { title: "单位数量 Q", dataIndex: "q", align: "right", render: (v: number) => fx(v, 2) },
          { title: "风险预算", dataIndex: "budget", align: "right", render: (v: number) => fx(v, 2) },
          { title: "净盈亏", dataIndex: "pnl", align: "right", render: (v: number | null) => (v === null ? "—" : <span className={v >= 0 ? "pos" : "neg"}>{(v >= 0 ? "+" : "") + fx(v, 2)}</span>) },
          { title: "收益（R）", dataIndex: "r", align: "right", render: (v: number | null) => (v === null ? "—" : <span className={-v >= 0 ? "pos" : "neg"}>{(-v >= 0 ? "+" : "") + fx(-v, 2)}R</span>) },
          { title: "退出方式", dataIndex: "exit", render: (v: string | null) => (v ? (exitText[v] ?? v) : "—") },
        ]}
      />
    </section>
  );
}

function RecentJudgments({ judgments, t }: { judgments: Record<string, Json>[]; t: (v: Json) => string }) {
  const recent = [...judgments].sort((a, b) => String(b.at).localeCompare(String(a.at))).slice(0, 6);
  return (
    <div>
      <div className="rp-h">最近研判</div>
      <ul className="recent">
        {recent.map((j) => (
          <li key={String(j.judgment_id)}>
            <Tag color={j.ok !== true ? "default" : j.direction === "SHORT" ? "red" : j.direction === "LONG" ? "green" : "blue"}>
              {j.ok === true ? `${directionText[String(j.direction)] ?? j.direction} ${j.confidence}` : "失败"}
            </Tag>
            <Typography.Text type="secondary">{t(j.at)}</Typography.Text>
            <div className="recent-text">{text(j.thesis) ?? text(j.reason) ?? ""}</div>
          </li>
        ))}
      </ul>
    </div>
  );
}
