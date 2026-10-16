import { useEffect, useState } from "react";

// S4-019 / P4 design 17.5. Reference values from the venue's public push;
// never accounting facts and never sent anywhere.
export type LiveState =
  | "OFF"
  | "CONNECTING"
  | "LIVE"
  | "STALE"
  | "UNAVAILABLE"
  | "ENDED";
export type LiveMark = { at: string; price: string };
export type LiveKline = {
  open_at: string;
  close_at: string;
  open: string;
  high: string;
  low: string;
  close: string;
  final: boolean;
};
export type LiveMarket = {
  state: LiveState;
  code: string | null;
  at: string | null;
  mark: LiveMark | null;
  kline: LiveKline | null;
};

const off: LiveMarket = { state: "OFF", code: null, at: null, mark: null, kline: null };

function visibleNow(): boolean {
  return typeof document === "undefined" || document.visibilityState !== "hidden";
}

// Opens the stream only while the page is visible; a closed stream that
// never delivered an event means the binding is not applicable (or the
// session is gone), which EventSource does not retry.
export function useLiveMarket(selectionId: string | null): LiveMarket {
  const [live, setLive] = useState<LiveMarket>(off);
  const [visible, setVisible] = useState(visibleNow());
  useEffect(() => {
    const onChange = () => setVisible(visibleNow());
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  useEffect(() => {
    setLive(off);
    if (!selectionId || !visible || typeof EventSource === "undefined") return;
    const source = new EventSource(
      `/api/v2/console/live?selection_id=${encodeURIComponent(selectionId)}`,
    );
    let received = false;
    const parse = (e: Event) => {
      received = true;
      try {
        return JSON.parse((e as MessageEvent).data);
      } catch {
        return null;
      }
    };
    setLive({ ...off, state: "CONNECTING" });
    source.addEventListener("state", (e) => {
      const v = parse(e);
      if (v && typeof v.state === "string")
        setLive((prev) => ({ ...prev, state: v.state, code: v.code ?? null, at: v.at ?? null }));
    });
    source.addEventListener("mark", (e) => {
      const v = parse(e);
      if (v && typeof v.price === "string") setLive((prev) => ({ ...prev, mark: v }));
    });
    source.addEventListener("kline", (e) => {
      const v = parse(e);
      if (v && typeof v.open_at === "string") setLive((prev) => ({ ...prev, kline: v }));
    });
    source.addEventListener("end", () => {
      source.close();
      setLive((prev) => ({ ...prev, state: "ENDED" }));
    });
    source.onerror = () => {
      if (source.readyState === EventSource.CLOSED)
        setLive((prev) =>
          received
            ? { ...prev, state: "UNAVAILABLE" }
            : { ...off, state: "UNAVAILABLE", code: "LIVE_NOT_APPLICABLE" },
        );
      else setLive((prev) => ({ ...prev, state: prev.mark ? "STALE" : "CONNECTING" }));
    };
    return () => source.close();
  }, [selectionId, visible]);
  return live;
}

export const liveStateText: Record<LiveState, string> = {
  OFF: "未连接",
  CONNECTING: "连接中",
  LIVE: "实时",
  STALE: "过期",
  UNAVAILABLE: "不可用",
  ENDED: "已结束",
};
