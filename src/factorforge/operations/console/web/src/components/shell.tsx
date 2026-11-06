// One header: the instance switch on the left, the two pages, display zone,
// theme and logout on the right. The console stays read only.
import { useEffect, useRef } from "react";
import { Link, Outlet, useRouterState } from "@tanstack/react-router";
import { Button, Empty, Layout, Segmented, Space, Spin, Tag, Tooltip, Typography } from "antd";
import { LogoutOutlined, MoonOutlined, SunOutlined } from "@ant-design/icons";
import { request, ApiError, type Selection } from "../api/client";
import { Login } from "../features/login";
import { DisplayZone } from "../features/display-settings";
import { useSession } from "../features/session";
import { useTheme } from "../features/theme";

export const pages = [
  ["flow", "实时流程"],
  ["market", "行情与交易"],
] as const;

export function instanceName(s: Selection): string {
  return s.instance_id.replace(/^soxl-/, "").toUpperCase();
}

export function Shell() {
  const { session, loaded, selection, selections, choose, invalidate } = useSession();
  const { mode, toggle } = useTheme();
  const path = useRouterState({ select: (s) => s.location.pathname });
  // S4-001: right after login the first allowed SIM instance opens; a scope
  // that later becomes invalid is not replaced by another one, and a LIVE
  // scope is never opened by itself. Nothing is stored.
  const opened = useRef<string | null>(null);
  useEffect(() => {
    if (!session || selection || selections.length === 0 || opened.current === session.user_id) return;
    opened.current = session.user_id;
    const first = selections.find((s) => s.environment !== "LIVE");
    if (first) choose(first);
  }, [session, selection, selections]);
  if (!loaded)
    return (
      <main className="loading" role="status">
        <Spin /> 正在恢复会话…
      </main>
    );
  if (!session) return <Login />;
  async function logout() {
    try {
      await request("/session", undefined, { method: "DELETE", headers: { "X-CSRF-Token": session?.csrf ?? "" } });
      invalidate();
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) invalidate();
    }
  }
  function pick(id: string) {
    choose(selections.find((x) => x.selection_id === id) ?? null);
  }
  return (
    <Layout className="app-shell">
      <Layout.Header className="topbar">
        <Space size="middle" wrap className="topbar-left">
          <span className="brand">
            <span className="brand-symbol">F</span>
          </span>
          <Segmented
            aria-label="切换实例"
            value={selection?.selection_id}
            onChange={(v) => pick(String(v))}
            options={selections.map((s) => ({
              value: s.selection_id,
              label: (
                <Tooltip title={`${s.environment} · ${s.trading_run_key.run_id} · ${s.instrument_key.instrument_id}`}>
                  <span className="instance-option">{instanceName(s)}</span>
                </Tooltip>
              ),
            }))}
          />
          {selection && (
            <Tag color={selection.environment === "LIVE" ? "red" : "blue"}>
              {selection.environment === "LIVE" ? "实盘" : "模拟"} · {selection.instrument_key.instrument_id}
            </Tag>
          )}
        </Space>
        <nav className="topnav" aria-label="页面">
          {pages.map(([to, title]) => (
            <Link key={to} to={"/" + to} className={path === "/" + to ? "topnav-item active" : "topnav-item"}>
              {title}
            </Link>
          ))}
        </nav>
        <Space className="topbar-right">
          <DisplayZone />
          <Tooltip title={mode === "dark" ? "浅色" : "深色"}>
            <Button type="text" aria-label="切换浅色或深色" icon={mode === "dark" ? <SunOutlined /> : <MoonOutlined />} onClick={toggle} />
          </Tooltip>
          <Typography.Text type="secondary" className="user-name">
            {session.display_name}
          </Typography.Text>
          <Tooltip title="退出">
            <Button type="text" aria-label="退出" icon={<LogoutOutlined />} onClick={logout} />
          </Tooltip>
        </Space>
      </Layout.Header>
      <Layout.Content id="main" tabIndex={-1} className="content">
        {selection ? (
          <Outlet />
        ) : (
          <Empty className="loading" description={selections.length === 0 ? "没有可查看的实例" : "请在左上角选择实例"} />
        )}
      </Layout.Content>
      <Layout.Footer className="app-footer">
        Factorforge 管理台 · 只读 ·{" "}
        <a href="/THIRD_PARTY_NOTICES.txt" target="_blank" rel="noreferrer">
          开源许可
        </a>
      </Layout.Footer>
    </Layout>
  );
}
