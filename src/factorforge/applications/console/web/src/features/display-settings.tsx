import { createContext, useContext, useState, type ReactNode } from "react";
const zones = ["UTC", "Asia/Tokyo", "Asia/Shanghai"] as const;
type Zone = (typeof zones)[number];
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
