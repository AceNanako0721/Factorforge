import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";
import type { Ctx } from "./replay-model";
import type { FlowNode, Health } from "./flow-model";
import { GO, LEAF, SYS, portNames, type Block, type System, type Wire } from "./replay-systems";

// S4-022: one level of the module hierarchy as a block diagram. Inports on the
// left, blocks in the middle, outports on the right; wires carry the frame's
// values. A click selects a block, a double click (or Enter) opens it.

const healthColor: Record<Health, string> = { ok: "var(--ok)", warn: "var(--warn)", error: "var(--error)", idle: "var(--idle)", loading: "var(--idle)" };
const healthText: Record<Health, string> = { ok: "正常", warn: "注意", error: "异常", idle: "未启用", loading: "读取中" };

type Geom = Block & { w: number; h: number };
function geom(b: Block): Geom {
  if (b.k === "in" || b.k === "out") return { ...b, w: 60, h: 26 };
  if (b.k === "const" || b.k === "from") return { ...b, ins: [], outs: [""], w: b.w ?? 70, h: b.h ?? 26 };
  return { ...b, w: b.w ?? 120, h: b.h ?? 70 };
}
function portXY(b: Block, side: "in" | "out", i: number): [number, number] {
  const g = geom(b);
  const list = side === "in" ? g.ins ?? [] : g.outs ?? [];
  const n = g.k === "in" || g.k === "out" ? 1 : list.length;
  const hdr = g.k === "sub" ? (g.st ? 40 : 26) : 0;
  return [side === "in" ? g.x : g.x + g.w, g.y + hdr + ((g.h - hdr) * (i + 1)) / (n + 1)];
}
function wirePath(w: Wire, sys: System) {
  const [ai, ap] = w.a.split("."), [bi, bp] = w.b.split(".");
  const A = sys.blocks.find((b) => b.id === ai)!, B = sys.blocks.find((b) => b.id === bi)!;
  const [x1, y1] = portXY(A, "out", +ap), [x2, y2] = portXY(B, "in", +bp);
  let pts: [number, number][];
  if (w.fb) pts = [[x1, y1], ...w.fb.map(([x, y], k): [number, number] => [x, y === null ? (k === 0 ? y1 : y2) : y]), [x2, y2]];
  else {
    const mx = w.mx ?? (x1 + x2) / 2;
    pts = [[x1, y1], [mx, y1], [mx, y2], [x2, y2]];
  }
  const segs: { len: number; at: [number, number] }[] = [];
  for (let k = 1; k < pts.length; k++) if (pts[k - 1][1] === pts[k][1]) segs.push({ len: Math.abs(pts[k][0] - pts[k - 1][0]), at: [(pts[k - 1][0] + pts[k][0]) / 2, pts[k][1]] });
  const pick = segs.find((g) => g.len >= 50) ?? segs.reduce((a, g) => (g.len > a.len ? g : a), { len: 0, at: [(x1 + x2) / 2, y1] as [number, number] });
  return { d: pts.map((p, k) => (k ? "L" : "M") + p[0] + " " + p[1]).join(" "), at: pick.at, room: pick.len, fb: !!w.fb };
}
const textWidth = (s: string) => [...s].reduce((a, ch) => a + (ch.charCodeAt(0) > 255 ? 10.5 : 6.4), 0);
const safe = (f: ((c: Ctx) => string) | undefined, c: Ctx | null) => {
  if (!f || !c) return "";
  try {
    return f(c);
  } catch {
    return "—";
  }
};
// Trim a value to the room it has; the full text is in the tooltip and the inspector.
function fitText(s: string, w: number) {
  if (textWidth(s) <= w) return s;
  let out = "";
  for (const ch of s) {
    if (textWidth(out + ch) + 8 > w) break;
    out += ch;
  }
  return out + "…";
}
function wrap(t: string, w: number, fs: number) {
  const per = Math.max(3, Math.floor((w - 26) / (fs + 1)));
  const n = Math.ceil(t.length / per), len = Math.ceil(t.length / n);
  const out: string[] = [];
  for (let p = 0; p < t.length; p += len) out.push(t.slice(p, p + len));
  return out;
}

export type DiagramProps = {
  system: string;
  ctx: Ctx | null;
  prev: Ctx | null;
  selected: string | null;
  onSelect: (id: string | null) => void;
  onEnter: (b: Block) => void;
  showValues: boolean;
  flowing: boolean;
  live: Map<string, FlowNode>;
};

type View = { x: number; y: number; w: number; h: number };

export function BlockDiagram({ system, ctx, prev, selected, onSelect, onEnter, showValues, flowing, live }: DiagramProps) {
  const sys = SYS[system];
  const svg = useRef<SVGSVGElement>(null);
  const fit = (): View => ({ x: -10, y: -24, w: sys.w + 20, h: sys.h + 40 });
  const [view, setView] = useState<View>(fit);
  const drag = useRef<{ x: number; y: number; v: View } | null>(null);
  useEffect(() => setView(fit()), [system]);
  const scale = (v: View) => {
    const r = svg.current!.getBoundingClientRect();
    return Math.max(v.w / r.width, v.h / r.height);
  };
  const onDown = (e: ReactPointerEvent<SVGSVGElement>) => {
    if ((e.target as Element).closest(".blk")) return;
    drag.current = { x: e.clientX, y: e.clientY, v: view };
    svg.current?.setPointerCapture(e.pointerId);
  };
  const onMove = (e: ReactPointerEvent<SVGSVGElement>) => {
    const d = drag.current;
    if (!d) return;
    const s = scale(d.v);
    setView({ ...d.v, x: d.v.x - (e.clientX - d.x) * s, y: d.v.y - (e.clientY - d.y) * s });
  };
  const onWheel = (e: ReactWheelEvent<SVGSVGElement>) => {
    const k = e.deltaY > 0 ? 1.12 : 1 / 1.12;
    const r = svg.current!.getBoundingClientRect(), s = scale(view);
    const mx = view.x + (e.clientX - r.left) * s, my = view.y + (e.clientY - r.top) * s;
    setView({ x: mx - (mx - view.x) * k, y: my - (my - view.y) * k, w: view.w * k, h: view.h * k });
  };
  useEffect(() => {
    // React registers wheel listeners as passive; zooming must not scroll the page.
    const el = svg.current;
    const stop = (e: WheelEvent) => e.preventDefault();
    el?.addEventListener("wheel", stop, { passive: false });
    return () => el?.removeEventListener("wheel", stop);
  }, []);
  const top = system === "root";
  return (
    <div className="bd-stage">
      <div className="bd-tools">
        <button className="bd-btn" onClick={() => setView(fit())}>适应</button>
      </div>
      <svg
        ref={svg}
        className={`bd${flowing ? " flowing" : ""}`}
        viewBox={`${view.x} ${view.y} ${view.w} ${view.h}`}
        role="img"
        aria-label={system === "root" ? "实时流程图" : `${sys.title} 模块图`}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={() => (drag.current = null)}
        onWheel={onWheel}
        onClick={() => onSelect(null)}
      >
        <defs>
          <marker id="bd-ar" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
            <path d="M0 0L10 5L0 10z" fill="var(--ok)" />
          </marker>
          <marker id="bd-arw" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
            <path d="M0 0L10 5L0 10z" fill="var(--warn)" />
          </marker>
        </defs>
        {system === "riskunit" && ctx && (
          <text x={20} y={10} className="bd-note">
            {ctx.frozen ? `Q 在开仓那一轮冻结（${ctx.fz.t.slice(5, 16).replace("T", " ")} UTC），之后不随行情变化` : `这一帧还没有冻结 Q：图中数值取这笔订单开仓那一轮（${ctx.fz.t.slice(5, 16).replace("T", " ")}）的输入`}
            {ctx.fz.gap ? `；${ctx.fz.gap}` : ""}
          </text>
        )}
        {sys.wires.map((w, i) => {
          const p = wirePath(w, sys);
          const val = safe(w.v, ctx), was = prev ? safe(w.v, prev) : val;
          const changed = !!prev && val !== was;
          return (
            <g key={i} className={`bd-wire${p.fb ? " fb" : ""}${changed ? " chg" : ""}`}>
              <title>{(w.l ?? "") + (val ? (w.l ? "：" : "") + val : "")}</title>
              <path d={p.d} markerEnd={changed ? "url(#bd-arw)" : "url(#bd-ar)"} />
              <path d={p.d} className="hit" />
              {w.l && textWidth(w.l) <= p.room + 12 && (
                <text x={p.at[0]} y={p.at[1] - 8} className="bd-wl" textAnchor="middle">
                  {w.l}
                </text>
              )}
              {showValues && val && textWidth(val) <= p.room - 6 && (
                <text x={p.at[0]} y={p.at[1] + 10} className="bd-wv" textAnchor="middle">
                  {val}
                </text>
              )}
            </g>
          );
        })}
        {sys.blocks.map((b0) => {
          const b = geom(b0);
          const node = top && b.live ? live.get(b.live) : undefined;
          const value = node && (!ctx || ctx.live) ? node.headline : safe(b.v, ctx);
          const before = prev ? safe(b.v, prev) : safe(b.v, ctx);
          const changed = !!prev && safe(b.v, ctx) !== before;
          const health: Health = node && (!ctx || ctx.live) ? node.health : b.warn && ctx && b.warn(ctx) ? "warn" : "ok";
          const cls = `blk ${b.k}${selected === b.id ? " sel" : ""}${changed ? " chg" : ""}${health === "warn" || health === "error" ? " " + health : ""}`;
          const rx = b.k === "in" || b.k === "out" ? 13 : b.k === "fn" ? 10 : b.k === "const" || b.k === "from" ? 6 : 12;
          const open = () => onEnter(b0);
          return (
            <g
              key={b.id}
              className={cls}
              tabIndex={0}
              role="button"
              aria-label={b.t}
              onClick={(e) => {
                e.stopPropagation();
                onSelect(b.id);
              }}
              onDoubleClick={(e) => {
                e.stopPropagation();
                open();
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") open();
              }}
            >
              {value && <title>{`${node?.title ?? b.t}：${value}`}</title>}
              <rect className="halo" x={b.x - 5} y={b.y - 5} width={b.w + 10} height={b.h + 10} rx={15} />
              <rect className="body" x={b.x} y={b.y} width={b.w} height={b.h} rx={rx} />
              {(b.k === "in" || b.k === "out") && (
                <>
                  <text x={b.x + b.w / 2} y={b.y + b.h / 2} className="bd-title" textAnchor="middle" dominantBaseline="middle">
                    {b.n}
                  </text>
                  <text x={b.x + b.w / 2} y={b.y + b.h + 12} className="bd-sub" textAnchor="middle" dominantBaseline="middle">
                    {b.t}
                  </text>
                  {showValues && value && (
                    <text x={b.x + b.w / 2} y={b.y + b.h + 25} className="bd-val" textAnchor="middle" dominantBaseline="middle">
                      {value}
                    </text>
                  )}
                </>
              )}
              {b.k === "from" && (
                <text x={b.x + b.w / 2} y={b.y + b.h / 2} className="bd-pl" textAnchor="middle" dominantBaseline="middle">
                  {b.t}
                </text>
              )}
              {b.k === "const" && (
                <>
                  <text x={b.x + b.w / 2} y={b.y + b.h / 2} className="bd-val" textAnchor="middle" dominantBaseline="middle">
                    {b.v ? safe(b.v, ctx) : b.val}
                  </text>
                  {b.lp === "left" ? (
                    <text x={b.x - 4} y={b.y + b.h / 2} className="bd-pl" textAnchor="end" dominantBaseline="middle">
                      {b.t}
                    </text>
                  ) : (
                    <text x={b.x + b.w / 2} y={b.y + b.h + 10} className="bd-pl" textAnchor="middle" dominantBaseline="middle">
                      {b.t}
                    </text>
                  )}
                </>
              )}
              {b.k === "fn" && <FnLabel b={b} value={showValues ? value : ""} />}
              {b.k === "sub" && (
                <>
                  {top && <circle cx={b.x + 14} cy={b.y + 16} r={7.5} fill={healthColor[health]} opacity={0.16} />}
                  {top && <circle cx={b.x + 14} cy={b.y + 16} r={4} fill={healthColor[health]} />}
                  <text x={b.x + (top ? 24 : b.w / 2)} y={b.y + 16} className="bd-title" textAnchor={top ? "start" : "middle"} dominantBaseline="middle">
                    {node?.title ?? b.t}
                  </text>
                  {b.st && (
                    <text x={b.x + (top ? 24 : b.w / 2)} y={b.y + 31} className="bd-sub" textAnchor={top ? "start" : "middle"} dominantBaseline="middle">
                      {b.st}
                    </text>
                  )}
                  {top && (
                    <text x={b.x + b.w - 10} y={b.y + 16} className="bd-stat" textAnchor="end" dominantBaseline="middle" fill={healthColor[health]}>
                      {healthText[health]}
                    </text>
                  )}
                  {showValues && value && (
                    // Tall blocks keep their port labels clear: the value goes under them.
                    <text x={b.x + b.w / 2} y={b.h > 90 ? b.y + b.h + 14 : b.y + b.h - 14} className="bd-val" textAnchor="middle" dominantBaseline="middle">
                      {fitText(value, b.h > 90 ? b.w + 60 : b.w - 16)}
                    </text>
                  )}
                </>
              )}
              {(b.ins ?? []).map((label, i) => {
                const [x, y] = portXY(b, "in", i);
                return (
                  <g key={"i" + i}>
                    <path className="tri" d={`M${x} ${y - 4}L${x + 6} ${y}L${x} ${y + 4}z`} />
                    {label && b.k === "sub" && b.h > 90 && (
                      <text x={x + 9} y={y} className="bd-pl" dominantBaseline="middle">
                        {label}
                      </text>
                    )}
                  </g>
                );
              })}
              {(b.outs ?? []).map((label, i) => {
                const [x, y] = portXY(b, "out", i);
                return (
                  <g key={"o" + i}>
                    <path className="tri" d={`M${x} ${y - 4}L${x + 6} ${y}L${x} ${y + 4}z`} />
                    {label && b.k === "sub" && b.h > 90 && (
                      <text x={x - 4} y={y} className="bd-pl" textAnchor="end" dominantBaseline="middle">
                        {label}
                      </text>
                    )}
                  </g>
                );
              })}
            </g>
          );
        })}
      </svg>
    </div>
  );
}

function FnLabel({ b, value }: { b: Geom; value: string }) {
  const small = b.w <= 90, fs = small ? 11 : 12.5;
  const lines = wrap(b.t, b.w, fs);
  const y0 = b.y + b.h / 2 - (small ? 0 : 9) - ((lines.length - 1) * (fs + 3)) / 2;
  return (
    <>
      {lines.map((ln, k) => (
        <text key={k} x={b.x + b.w / 2} y={y0 + k * (fs + 3)} className="bd-title" textAnchor="middle" dominantBaseline="middle" style={{ fontSize: fs }}>
          {ln}
        </text>
      ))}
      {value && (
        <text x={b.x + b.w / 2} y={small ? b.y + b.h + 13 : y0 + lines.length * (fs + 3) + 6} className="bd-val" textAnchor="middle" dominantBaseline="middle">
          {value}
        </text>
      )}
    </>
  );
}

// Formulas carry a few layout tags; everything else, including any value
// read from the records, is text.
export function formulaHtml(raw: string) {
  return raw
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/&lt;br&gt;/g, "<br>")
    .replace(/&lt;(\/?)sub&gt;/g, "<$1sub>")
    .replace(/&lt;span class="res"&gt;/g, '<span class="res">')
    .replace(/&lt;\/span&gt;/g, "</span>");
}

// The deepest level: inputs, the formula with the frame's numbers, outputs.
export function FunctionPage({ leaf, ctx }: { leaf: string; ctx: Ctx | null }) {
  const L = LEAF[leaf];
  if (!L) return null;
  let io: ReturnType<typeof L.io> = { in: [], out: [] };
  let num = "";
  if (!ctx) {
    io = portNames(L);
    num = "—";
  } else {
    try {
      io = L.io(ctx);
      num = L.num(ctx);
    } catch {
      num = "这一帧缺少计算所需的记录";
    }
  }
  return (
    <div className="fp">
      <div className="fp-grid">
        <div>
          <div className="fp-head">INPUT</div>
          {io.in.map(([k, v], i) => (
            <div className="fp-port" key={i}>
              <span>
                <span className="fp-n">in{i + 1}</span> {k}
              </span>
              <b>{String(v)}</b>
            </div>
          ))}
        </div>
        <div className="fp-card">
          <div className="fp-title">{L.title}</div>
          <div className="fp-sym" dangerouslySetInnerHTML={{ __html: formulaHtml(L.sym) }} />
          <div className="fp-num" dangerouslySetInnerHTML={{ __html: formulaHtml(num) }} />
        </div>
        <div>
          <div className="fp-head">OUTPUT</div>
          {io.out.map(([k, v], i) => (
            <div className="fp-port" key={i}>
              <span>
                <span className="fp-n">out{i + 1}</span> {k}
              </span>
              <b>{String(v)}</b>
            </div>
          ))}
        </div>
      </div>
      {!ctx && <p className="fp-go">选择一笔订单后显示这一步代入的数值</p>}
      <p className="fp-ex">{L.ex}</p>
      {L.src && <p className="fp-go">对应函数：{GO[L.src]}</p>}
      {L.src === "freeze" && ctx?.fz.gap && <p className="fp-go">{ctx.fz.gap}</p>}
    </div>
  );
}
