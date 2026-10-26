import { useEffect, useMemo, useRef, useState } from "react";
import type { LiveKline } from "./live-market";
import {
  createChart,
  createSeriesMarkers,
  CandlestickSeries,
  LineStyle,
  type AutoscaleInfo,
  type CandlestickData,
  type IChartApi,
  type IPriceLine,
  type IRange,
  type ISeriesApi,
  type ISeriesMarkersPluginApi,
  type SeriesMarker,
  type Time,
  type UTCTimestamp,
} from "lightweight-charts";
import { type Json, record } from "../api/client";
export function candleData(records: Json[]): CandlestickData<UTCTimestamp>[] {
  const result: CandlestickData<UTCTimestamp>[] = [];
  const seen = new Set<number>();
  for (const raw of records) {
    const c = record(raw);
    if (c.final !== true || typeof c.open_at !== "string") continue;
    const at = Date.parse(c.open_at) / 1000;
    const values = ["open", "high", "low", "close"].map((k) =>
      typeof c[k] === "string" ? Number(c[k]) : NaN,
    );
    if (
      !Number.isFinite(at) ||
      !Number.isInteger(at) ||
      seen.has(at) ||
      values.some((x) => !Number.isFinite(x) || x <= 0)
    )
      continue;
    seen.add(at);
    result.push({
      time: at as UTCTimestamp,
      open: values[0],
      high: values[1],
      low: values[2],
      close: values[3],
    });
  }
  return result.sort((a, b) => Number(a.time) - Number(b.time));
}
// Optional overlays for the S4-017 page: horizontal price lines and time
// markers. Both only draw recorded values; nothing is projected forward.
export type PriceLine = {
  price: number;
  title: string;
  color: string;
  dashed?: boolean;
};
export type TimeMarker = {
  at: string;
  text: string;
  color: string;
  shape: "arrowUp" | "arrowDown" | "circle";
};
// v2.14.8: like TradingView, the price axis fits the bars by default; the
// stop lines join the range only when the viewer asks for it.
export function scaleRange(info: AutoscaleInfo | null, lines: PriceLine[]): AutoscaleInfo | null {
  const prices = lines.map((l) => l.price).filter((p) => Number.isFinite(p) && p > 0);
  if (!info?.priceRange || prices.length === 0) return info;
  return {
    ...info,
    priceRange: {
      minValue: Math.min(info.priceRange.minValue, ...prices),
      maxValue: Math.max(info.priceRange.maxValue, ...prices),
    },
  };
}
// Wheel over the price axis: deltaY > 0 widens the range, < 0 narrows it,
// keeping the price under the cursor in place.
export function zoomRange(range: IRange<number>, anchor: number, deltaY: number): IRange<number> | null {
  const factor = Math.exp(Math.max(-200, Math.min(200, deltaY)) * 0.002);
  const from = anchor - (anchor - range.from) * factor;
  const to = anchor + (range.to - anchor) * factor;
  if (!Number.isFinite(from) || !Number.isFinite(to) || to - from <= 1e-9) return null;
  return { from, to };
}
// Lines outside the bars' high/low are listed under the chart so a far stop is
// never silently off screen.
export function offChart(lines: PriceLine[], data: CandlestickData<UTCTimestamp>[]): PriceLine[] {
  if (data.length === 0) return [];
  const low = Math.min(...data.map((c) => c.low));
  const high = Math.max(...data.map((c) => c.high));
  return lines.filter((l) => Number.isFinite(l.price) && (l.price < low || l.price > high));
}
export function MarketChart({
  candles,
  lines = [],
  markers = [],
  live = null,
}: {
  candles: Json[];
  lines?: PriceLine[];
  markers?: TimeMarker[];
  live?: LiveKline | null;
}) {
  const div = useRef<HTMLDivElement>(null);
  const chartRef = useRef<IChartApi | null>(null);
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const markerRef = useRef<ISeriesMarkersPluginApi<Time> | null>(null);
  const priceLines = useRef<IPriceLine[]>([]);
  const fitted = useRef(false);
  const lastFinal = useRef(0);
  const liveRef = useRef<LiveKline | null>(live);
  liveRef.current = live;
  const linesRef = useRef(lines);
  linesRef.current = lines;
  const [withLines, setWithLines] = useState(false);
  const withLinesRef = useRef(withLines);
  withLinesRef.current = withLines;
  const data = useMemo(() => candleData(candles), [candles]);
  // The chart is built once; data, lines and markers update in place so a
  // refresh keeps the viewer's zoom and scroll.
  useEffect(() => {
    const host = div.current;
    if (!host) return;
    const chart = createChart(host, {
      height: 320,
      autoSize: true,
      layout: {
        background: { color: "#ffffff" },
        textColor: "#34413b",
        attributionLogo: true,
      },
      grid: {
        vertLines: { color: "#edf0ea" },
        horzLines: { color: "#edf0ea" },
      },
      timeScale: { timeVisible: true, secondsVisible: false },
    });
    const series = chart.addSeries(CandlestickSeries, {
      upColor: "#267b59",
      downColor: "#b15447",
      borderVisible: false,
      wickUpColor: "#267b59",
      wickDownColor: "#b15447",
      autoscaleInfoProvider: (base: () => AutoscaleInfo | null) =>
        scaleRange(base(), withLinesRef.current ? linesRef.current : []),
    });
    const onWheel = (e: WheelEvent) => {
      const scale = chart.priceScale("right");
      const rect = host.getBoundingClientRect();
      if (e.clientX < rect.right - scale.width()) return;
      const range = scale.getVisibleRange();
      const anchor = series.coordinateToPrice(e.clientY - rect.top);
      if (!range || anchor === null) return;
      e.preventDefault();
      e.stopPropagation();
      const next = zoomRange(range, anchor, e.deltaY);
      if (!next) return;
      scale.setAutoScale(false);
      scale.setVisibleRange(next);
    };
    host.addEventListener("wheel", onWheel, { passive: false, capture: true });
    chartRef.current = chart;
    seriesRef.current = series;
    markerRef.current = createSeriesMarkers(series, []);
    return () => {
      host.removeEventListener("wheel", onWheel, { capture: true });
      chartRef.current = null;
      seriesRef.current = null;
      markerRef.current = null;
      priceLines.current = [];
      fitted.current = false;
      chart.remove();
    };
  }, []);
  useEffect(() => {
    const chart = chartRef.current;
    const series = seriesRef.current;
    if (!chart || !series) return;
    series.setData(data);
    if (!fitted.current && data.length > 0) {
      chart.timeScale().fitContent();
      fitted.current = true;
    }
    lastFinal.current = data.length > 0 ? Number(data[data.length - 1].time) : 0;
    drawLive(series, liveRef.current, lastFinal.current);
  }, [data]);
  useEffect(() => {
    const series = seriesRef.current;
    if (!series) return;
    for (const l of priceLines.current) series.removePriceLine(l);
    priceLines.current = [];
    for (const l of lines)
      if (Number.isFinite(l.price) && l.price > 0)
        priceLines.current.push(
          series.createPriceLine({
            price: l.price,
            color: l.color,
            title: l.title,
            lineWidth: 1,
            lineStyle: l.dashed ? LineStyle.Dashed : LineStyle.Solid,
            axisLabelVisible: true,
          }),
        );
  }, [lines]);
  useEffect(() => {
    if (!markerRef.current) return;
    const placed: SeriesMarker<UTCTimestamp>[] = [];
    if (data.length > 0) {
      // Markers snap to the bar that contains their time.
      const first = Number(data[0].time);
      const step = data.length > 1 ? Number(data[1].time) - first : 60;
      for (const m of markers) {
        const at = Date.parse(m.at) / 1000;
        if (!Number.isFinite(at) || at < first) continue;
        const bar = data.findLast((c) => Number(c.time) <= at);
        if (!bar || at - Number(bar.time) > step * 2) continue;
        placed.push({
          time: bar.time,
          position: m.shape === "arrowUp" ? "belowBar" : "aboveBar",
          shape: m.shape,
          color: m.color,
          text: m.text,
        });
      }
      placed.sort((a, b) => Number(a.time) - Number(b.time));
    }
    markerRef.current.setMarkers(placed);
  }, [data, markers]);
  useEffect(() => {
    chartRef.current?.priceScale("right").setAutoScale(true);
  }, [withLines]);
  useEffect(() => {
    if (seriesRef.current) drawLive(seriesRef.current, live, lastFinal.current);
  }, [live]);
  const outside = withLines ? [] : offChart(lines, data);
  return (
    <div>
      <div className="chart-tools">
        <button type="button" onClick={() => chartRef.current?.priceScale("right").setAutoScale(true)}>
          纵轴自动
        </button>
        <button type="button" onClick={() => chartRef.current?.timeScale().fitContent()}>
          显示全部时间
        </button>
        <label>
          <input type="checkbox" checked={withLines} onChange={(e) => setWithLines(e.target.checked)} />
          纵轴包含全部止损线
        </label>
      </div>
      <div
        ref={div}
        aria-label="已记录最终K线"
        role="img"
        className="chart-host"
        style={{ height: 320, position: "relative" }}
      />
      {outside.length > 0 && (
        <p className="muted">
          K 线范围外：{outside.map((l) => `${l.title} ${l.price}`).join("、")}（勾选“纵轴包含全部止损线”可显示）
        </p>
      )}
      <p className="muted">
        滚轮在图内缩放时间，在右侧价格轴上缩放纵轴；拖动价格轴也可缩放纵轴，双击价格轴或点“纵轴自动”恢复。仅显示产品实际记录的最终 K 线；坐标为近似值，表格保留金额原值。图表：
        <a href="https://www.tradingview.com/" target="_blank" rel="noreferrer">
          TradingView Lightweight Charts
        </a>
      </p>
    </div>
  );
}

// S4-019: the forming bar from the venue push, drawn translucent after the
// last recorded final bar; it is replaced once P1 records the final bar.
function drawLive(
  series: ISeriesApi<"Candlestick">,
  live: LiveKline | null,
  lastFinal: number,
) {
  if (!live) return;
  const at = Date.parse(live.open_at) / 1000;
  const values = [live.open, live.high, live.low, live.close].map(Number);
  if (!Number.isInteger(at) || at <= lastFinal || values.some((x) => !Number.isFinite(x) || x <= 0)) return;
  const up = values[3] >= values[0];
  const color = up ? "rgba(38, 123, 89, 0.45)" : "rgba(177, 84, 71, 0.45)";
  try {
    series.update({
      time: at as UTCTimestamp,
      open: values[0],
      high: values[1],
      low: values[2],
      close: values[3],
      color,
      wickColor: color,
      borderColor: color,
    });
  } catch {
    // An older bar than the series tail cannot be updated; skip it.
  }
}
