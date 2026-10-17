import { useEffect, useRef } from "react";
import type { LiveKline } from "./live-market";
import {
  createChart,
  createSeriesMarkers,
  CandlestickSeries,
  LineStyle,
  type AutoscaleInfo,
  type CandlestickData,
  type ISeriesApi,
  type SeriesMarker,
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
  const seriesRef = useRef<ISeriesApi<"Candlestick"> | null>(null);
  const lastFinal = useRef(0);
  const liveRef = useRef<LiveKline | null>(live);
  liveRef.current = live;
  useEffect(() => {
    if (!div.current) return;
    const chart = createChart(div.current, {
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
      // The price axis spans the drawn lines too, so stops far from the
      // recent bars stay on screen instead of being clipped.
      autoscaleInfoProvider: (base: () => AutoscaleInfo | null) => {
        const info = base();
        const prices = lines
          .map((l) => l.price)
          .filter((p) => Number.isFinite(p) && p > 0);
        if (!info?.priceRange || prices.length === 0) return info;
        return {
          ...info,
          priceRange: {
            minValue: Math.min(info.priceRange.minValue, ...prices),
            maxValue: Math.max(info.priceRange.maxValue, ...prices),
          },
        };
      },
    });
    const data = candleData(candles);
    series.setData(data);
    for (const l of lines)
      if (Number.isFinite(l.price) && l.price > 0)
        series.createPriceLine({
          price: l.price,
          color: l.color,
          title: l.title,
          lineWidth: 1,
          lineStyle: l.dashed ? LineStyle.Dashed : LineStyle.Solid,
          axisLabelVisible: true,
        });
    if (data.length > 0 && markers.length > 0) {
      // Markers snap to the bar that contains their time.
      const first = Number(data[0].time);
      const step = data.length > 1 ? Number(data[1].time) - first : 60;
      const placed: SeriesMarker<UTCTimestamp>[] = [];
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
      createSeriesMarkers(series, placed);
    }
    chart.timeScale().fitContent();
    seriesRef.current = series;
    lastFinal.current = data.length > 0 ? Number(data[data.length - 1].time) : 0;
    drawLive(series, liveRef.current, lastFinal.current);
    return () => {
      seriesRef.current = null;
      chart.remove();
    };
  }, [candles, lines, markers]);
  useEffect(() => {
    if (seriesRef.current) drawLive(seriesRef.current, live, lastFinal.current);
  }, [live]);
  return (
    <div>
      <div
        ref={div}
        aria-label="已记录最终K线"
        role="img"
        className="chart-host"
        style={{ height: 320, position: "relative" }}
      />
      <p className="muted">
        仅显示产品实际记录的最终 K 线；坐标为近似值，表格保留金额原值。图表：
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
