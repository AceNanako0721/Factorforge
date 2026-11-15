import { useEffect, useRef, useState } from "react";
import { keyEvents, type Replay, type ReplayEvent } from "./replay-model";
import { fx, hm, jName } from "./replay-systems";

// S4-022: the timeline under the diagram. Price, entry and close stop on top,
// event nodes below; the playhead picks the frame every module shows.

const eventText: Record<string, (e: ReplayEvent) => string> = {
  J: (e) => (e.ok ? `研判转为 ${jName(String(e.dir))}/${e.conf}` : "研判失败"),
  F: (e) => `${e.side === "BUY" ? "买入" : "卖出"} ${fx(e.q ?? null, 2)} @ ${fx(e.p ?? null, 2)}`,
  OPEN: (e) => `批次开仓 · 收盘止损 ${fx(e.cs ?? null, 2)}`,
  CLOSE: () => "批次平仓",
  TRAIL: (e) => `止损移到 ${fx(e.cs ?? null, 2)}`,
  TRANSFER: () => "转为基线",
  L: (e) => `档位 ${e.from}→${e.to}`,
  BASELINE_CLEARED: () => "基线清除",
  CLOSE_EXIT: () => "收盘止损退出",
};
export const describeEvent = (e: ReplayEvent) => (eventText[e.k] ? eventText[e.k](e) : e.k);

type Chip = ReplayEvent & { n?: number; end?: string };
function chips(events: ReplayEvent[]): Chip[] {
  const out: Chip[] = [];
  for (const e of keyEvents(events)) {
    const last = out[out.length - 1];
    if (e.k === "J" && !e.ok && last && last.k === "J" && !last.ok) {
      last.n = (last.n ?? 1) + 1;
      last.end = e.t;
    } else out.push({ ...e });
  }
  return out;
}

export type TimelineProps = {
  replay: Replay;
  index: number;
  onSeek: (i: number) => void;
  playing: boolean;
  onPlay: (on: boolean) => void;
  speed: number;
  onSpeed: (s: number) => void;
  onLive: () => void;
  live: boolean;
};

export function ReplayTimeline({ replay, index, onSeek, playing, onPlay, speed, onSpeed, onLive, live }: TimelineProps) {
  const { frames, events, order } = replay;
  const box = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(900);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(Math.max(560, el.clientWidth)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  if (frames.length === 0) return <div className="tl-empty">这笔订单没有可回放的决策记录</div>;
  const W = width, H = 150;
  const t0 = Date.parse(frames[0].t + "Z"), t1 = Date.parse(frames[frames.length - 1].t + "Z");
  const X = (t: string) => 40 + ((Date.parse(t + (t.endsWith("Z") ? "" : "Z")) - t0) / Math.max(1, t1 - t0)) * (W - 60);
  const prices = frames.map((f) => f.px).filter((v): v is number => v !== null);
  const stops = frames.map((f) => f.bt?.cs).filter((v): v is number => typeof v === "number" && v > 0);
  const lo = Math.min(...prices, ...stops) * 0.995, hi = Math.max(...prices, ...stops) * 1.005;
  const Y = (v: number) => 14 + (1 - (v - lo) / (hi - lo || 1)) * 76;
  const f = frames[Math.min(index, frames.length - 1)];
  const entry = frames.find((x) => x.bt)?.bt?.entry;
  const hours = [...new Set(frames.map((x) => x.t.slice(0, 13)))];
  const step = Math.max(1, Math.ceil(hours.length / 8));
  const seek = (t: string) => {
    let best = 0;
    frames.forEach((x, k) => {
      if (x.t <= t) best = k;
    });
    onSeek(best);
  };
  const at = (clientX: number, el: SVGSVGElement) => {
    const r = el.getBoundingClientRect();
    const x = (clientX - r.left) * (W / r.width);
    const t = t0 + Math.max(0, Math.min(1, (x - 40) / (W - 60))) * (t1 - t0);
    seek(new Date(t).toISOString().slice(0, 19));
  };
  const keys = keyEvents(events).map((e) => e.t);
  const jump = (dir: number) => {
    const t = dir > 0 ? keys.find((x) => x > f.t) : [...keys].reverse().find((x) => x < f.t);
    if (t) {
      onPlay(false);
      seek(t);
    }
  };
  const near = (e: Chip) => (f.t >= e.t && f.t <= (e.end ?? e.t)) || Math.abs(Date.parse(e.t + "Z") - Date.parse(f.t + "Z")) < 300_000;
  return (
    <div className="tl">
      <div className="tl-bar">
        <button className="bd-btn" title="回到开始" onClick={() => { onPlay(false); onSeek(0); }}>⏮</button>
        <button className="bd-btn" title="上一个事件" onClick={() => jump(-1)}>◀ 事件</button>
        <button className="bd-btn" title="播放 / 暂停" onClick={() => onPlay(!playing)}>{playing ? "⏸ 暂停" : "▶ 播放"}</button>
        <button className="bd-btn" title="下一个事件" onClick={() => jump(1)}>事件 ▶</button>
        <label className="tl-note" htmlFor="tl-speed">速度</label>
        <select id="tl-speed" className="bd-btn" value={speed} onChange={(e) => onSpeed(Number(e.target.value))}>
          {[1, 2, 4, 8, 16, 32].map((s) => (
            <option key={s} value={s}>{s}×</option>
          ))}
        </select>
        <button className={`bd-btn${live ? " on" : ""}`} disabled={!!order.closed} title="跳到最新并跟随" onClick={onLive}>实时</button>
        <span className="tl-time">{hm(f.t)} UTC</span>
        <span className="tl-note">第 {index + 1} / {frames.length} 轮（每 5 分钟一轮决策）</span>
      </div>
      <div className="tl-wrap" ref={box}>
        <svg
          className="tl-svg"
          viewBox={`0 0 ${W} ${H}`}
          aria-label="时间轴"
          onPointerDown={(e) => {
            onPlay(false);
            (e.currentTarget as SVGSVGElement).setPointerCapture(e.pointerId);
            at(e.clientX, e.currentTarget);
          }}
          onPointerMove={(e) => {
            if (e.buttons) at(e.clientX, e.currentTarget);
          }}
        >
          <rect className="tl-lane" x={40} y={98} width={W - 60} height={40} rx={4} />
          <g className="tl-axis">
            <text x={4} y={Y(hi) + 4}>{fx(hi, 1)}</text>
            <text x={4} y={Y(lo)}>{fx(lo, 1)}</text>
            {hours.map((h, i) => {
              if (i % step) return null;
              const x = X(h + ":00:00");
              if (x < 40) return null;
              return (
                <g key={h}>
                  <line x1={x} x2={x} y1={10} y2={138} />
                  <text x={x + 3} y={146}>{h.slice(5, 10).replace("-", "/")} {h.slice(11)}:00</text>
                </g>
              );
            })}
          </g>
          {entry !== undefined && (
            <>
              <line className="tl-entry" x1={40} x2={W - 20} y1={Y(entry)} y2={Y(entry)} />
              <text className="tl-lab" x={W - 22} y={Y(entry) - 4} textAnchor="end">开仓 {fx(entry, 2)}</text>
            </>
          )}
          <path className="tl-px" d={frames.filter((x) => x.px !== null).map((x, i) => (i ? "L" : "M") + X(x.t).toFixed(1) + " " + Y(x.px as number).toFixed(1)).join(" ")} />
          {stops.length > 0 && (
            <path className="tl-cs" d={frames.filter((x) => x.bt && x.bt.cs > 0).map((x, i) => (i ? "L" : "M") + X(x.t).toFixed(1) + " " + Y(x.bt!.cs).toFixed(1)).join(" ")} />
          )}
          {events.map((e, i) => {
            const x = X(e.t);
            let shape;
            if (e.k === "J") {
              const col = !e.ok ? "var(--idle)" : e.dir === "LONG" ? "var(--ok)" : e.dir === "SHORT" ? "var(--error)" : "var(--idle)";
              shape = <line x1={x} x2={x} y1={102} y2={112} stroke={col} strokeWidth={e.ok ? 2 : 1} strokeDasharray={e.ok ? undefined : "2 2"} />;
            } else if (e.k === "F") shape = <path d={`M${x} 116 l5 6 l-5 6 l-5 -6z`} fill={e.side === "BUY" ? "var(--ok)" : "var(--error)"} />;
            else if (e.k === "OPEN" || e.k === "CLOSE") shape = <circle cx={x} cy={122} r={6} fill="var(--surface)" stroke="var(--brand)" strokeWidth={2.5} />;
            else if (e.k === "TRAIL" || e.k === "TRANSFER") shape = <path d={`M${x - 5} 134 l5 -8 l5 8z`} fill="var(--error)" />;
            else if (e.k === "L") shape = <rect x={x - 4} y={(e.to ?? 0) > (e.from ?? 0) ? 100 : 128} width={8} height={8} fill="var(--warn)" />;
            else shape = <circle cx={x} cy={132} r={3} fill="var(--muted)" />;
            return (
              <g key={i}>
                <title>{`${hm(e.t)} ${describeEvent(e)}`}</title>
                {shape}
              </g>
            );
          })}
          <line className="tl-ph" x1={X(f.t)} x2={X(f.t)} y1={8} y2={140} />
          {f.px !== null && <circle cx={X(f.t)} cy={Y(f.px)} r={3.5} fill="var(--brand)" />}
          <text className="tl-phl" x={Math.min(X(f.t) + 5, W - 70)} y={9}>{fx(f.px, 2)}</text>
        </svg>
      </div>
      <div className="tl-events">
        {chips(events).map((e, i) => (
          <button key={i} className={`tl-chip${near(e) ? " cur" : ""}`} onClick={() => { onPlay(false); seek(e.t); }}>
            {hm(e.t).slice(6)}
            {e.end ? "–" + hm(e.end).slice(6) : ""} {e.n ? `研判失败 ×${e.n}` : describeEvent(e)}
          </button>
        ))}
      </div>
    </div>
  );
}
