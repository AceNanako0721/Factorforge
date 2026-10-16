import { useEffect, useRef } from "react";
import {
  createChart,
  createSeriesMarkers,
  CandlestickSeries,
  LineStyle,
  type CandlestickData,
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
}: {
  candles: Json[];
  lines?: PriceLine[];
  markers?: TimeMarker[];
}) {
  const div = useRef<HTMLDivElement>(null);
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
    return () => chart.remove();
  }, [candles, lines, markers]);
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
