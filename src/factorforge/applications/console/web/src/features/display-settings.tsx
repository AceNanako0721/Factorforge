import { createContext, useContext, useState, type ReactNode } from "react";
const zones = ["UTC", "Asia/Tokyo", "Asia/Shanghai"] as const;
export type Zone = (typeof zones)[number];
const DisplayContext = createContext<{
  zone: Zone;
  setZone: (s: Zone) => void;
} | null>(null);
export function DisplayProvider({ children }: { children: ReactNode }) {
  const [zone, change] = useState<Zone>(() => {
    try {
      const v = localStorage.getItem("factorforge-display-zone");
      if (zones.includes(v as Zone)) return v as Zone;
    } catch {}
    return "UTC";
  });
  const setZone = (s: Zone) => {
    change(s);
    try {
      localStorage.setItem("factorforge-display-zone", s);
    } catch {}
  };
  return (
    <DisplayContext.Provider value={{ zone, setZone }}>
      {children}
    </DisplayContext.Provider>
  );
}
export function useDisplay() {
  const settings = useContext(DisplayContext);
  if (!settings) throw new Error("DISPLAY_CONTEXT");
  return settings;
}
export function DisplayZone() {
  const { zone, setZone } = useDisplay();
  return (
    <label className="zone-select">
      <span>显示时区</span>
      <select
        aria-label="显示时区"
        value={zone}
        onChange={(e) => setZone(e.target.value as Zone)}
      >
        {zones.map((z) => (
          <option key={z}>{z}</option>
        ))}
      </select>
    </label>
  );
}
export function displayTime(value: string, zone: Zone): string {
  if (
    zone === "UTC" ||
    !/^\d{4}-\d\d-\d\dT.*Z$/.test(value) ||
    !Number.isFinite(Date.parse(value))
  )
    return value;
  return `${new Intl.DateTimeFormat("zh-CN", { timeZone: zone, dateStyle: "short", timeStyle: "medium", hourCycle: "h23" }).format(new Date(value))} · ${zone}`;
}
// v2.19.6: chart labels follow the display zone too. Chart data stays in UTC
// seconds; only the axis ticks and the crosshair label are formatted here.
function parts(seconds: number, zone: Zone): Record<string, string> {
  const out: Record<string, string> = {};
  for (const p of new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23" }).formatToParts(new Date(seconds * 1000)))
    out[p.type] = p.value;
  return out;
}
export function chartTime(seconds: number, zone: Zone): string {
  const p = parts(seconds, zone);
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute} · ${zone}`;
}
// kind follows lightweight-charts TickMarkType: 0 year, 1 month, 2 day, 3+ time.
export function chartTick(seconds: number, kind: number, zone: Zone): string {
  const p = parts(seconds, zone);
  if (kind === 0) return p.year;
  if (kind === 1) return `${p.year}-${p.month}`;
  if (kind === 2) return `${p.month}-${p.day}`;
  return `${p.hour}:${p.minute}`;
}
