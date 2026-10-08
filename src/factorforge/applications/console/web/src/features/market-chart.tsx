import { useEffect, useRef } from "react";
import {
  createChart,
  CandlestickSeries,
  type CandlestickData,
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
export function MarketChart({ candles }: { candles: Json[] }) {
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
    series.setData(candleData(candles));
    chart.timeScale().fitContent();
    return () => chart.remove();
  }, [candles]);
  return (
    <div>
      <div ref={div} aria-label="已记录最终K线" role="img" />
      <p className="muted">
        仅显示产品实际记录的最终 K 线；坐标为近似值，表格保留金额原值。图表：
        <a href="https://www.tradingview.com/" target="_blank" rel="noreferrer">
          TradingView Lightweight Charts
        </a>
      </p>
    </div>
  );
}
