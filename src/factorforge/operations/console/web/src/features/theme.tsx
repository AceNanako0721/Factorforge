import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { ConfigProvider, theme as antTheme, App as AntApp } from "antd";
import zhCN from "antd/locale/zh_CN";

// Light or dark display, kept in the browser like the display zone (8.7).
type Mode = "light" | "dark";
const KEY = "factorforge-theme";
const ThemeContext = createContext<{ mode: Mode; toggle: () => void }>({ mode: "light", toggle: () => {} });
export const useTheme = () => useContext(ThemeContext);

function initial(): Mode {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark") return v;
  } catch {
    // Storage may be unavailable; fall back to the system preference.
  }
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<Mode>(initial);
  useEffect(() => {
    document.documentElement.dataset.theme = mode;
    try {
      localStorage.setItem(KEY, mode);
    } catch {
      // Only this page keeps the choice.
    }
  }, [mode]);
  return (
    <ThemeContext.Provider value={{ mode, toggle: () => setMode(mode === "dark" ? "light" : "dark") }}>
      <ConfigProvider
        locale={zhCN}
        theme={{
          algorithm: mode === "dark" ? antTheme.darkAlgorithm : antTheme.defaultAlgorithm,
          token: { colorPrimary: "#2f6f5a", borderRadius: 8, fontFamily: "Inter, 'Noto Sans SC', system-ui, sans-serif" },
        }}
      >
        <AntApp>{children}</AntApp>
      </ConfigProvider>
    </ThemeContext.Provider>
  );
}
