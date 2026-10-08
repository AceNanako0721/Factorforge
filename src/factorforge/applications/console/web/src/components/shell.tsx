// Layout adapted from shadcn-admin authenticated-layout at the frozen MIT ref.
// Local session replaces Clerk; no demo edit controls or trading actions.
import { useState } from "react";
import { Link, Outlet } from "@tanstack/react-router";
import { request, ApiError } from "../api/client";
import { Button } from "./button";
import { Login } from "../features/login";
import { pages } from "../features/page";
import { DisplayZone } from "../features/display-settings";
import { useSession } from "../features/session";
export function Shell() {
  const { session, loaded, selection, selections, choose, invalidate } =
    useSession();
  const [menu, setMenu] = useState(false);
  if (!loaded)
    return (
      <main className="loading" role="status">
        正在恢复会话…
      </main>
    );
  if (!session) return <Login />;
  async function logout() {
    try {
      await request("/session", undefined, {
        method: "DELETE",
        headers: { "X-CSRF-Token": session?.csrf ?? "" },
      });
      invalidate();
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) invalidate();
    }
  }
  return (
    <div className="app-shell">
      <a className="skip-link" href="#main">
        跳到内容
      </a>
      <aside className={menu ? "sidebar mobile-open" : "sidebar"}>
        <Link to="/overview" className="brand">
          <span className="brand-symbol">F</span>
          <span>
            FACTORFORGE<small>观察与追溯</small>
          </span>
        </Link>
        <p className="nav-label">工作区 / READ ONLY</p>
        <nav aria-label="主要导航">
          {pages.map(([path, title, icon]) => (
            <Link
              key={path}
              to={"/" + path}
              activeProps={{ className: "nav-item active" }}
              inactiveProps={{ className: "nav-item" }}
              onClick={() => setMenu(false)}
            >
              <span aria-hidden="true">{icon}</span>
              {title}
            </Link>
          ))}
        </nav>
        <div className="sidebar-note">
          事实与判断分别呈现
          <br />
          未知状态保留未知
        </div>
      </aside>
      <div className="workspace">
        <header className="topbar">
          <Button
            variant="ghost"
            className="menu-toggle"
            onClick={() => setMenu(!menu)}
            aria-expanded={menu}
            aria-label="展开导航"
          >
            ☰
          </Button>
          <label className="scope-select">
            <span>查看范围</span>
            <select
              aria-label="查看范围"
              value={selection?.selection_id ?? ""}
              onChange={(e) =>
                choose(
                  selections.find((s) => s.selection_id === e.target.value) ??
                    null,
                )
              }
            >
              <option value="">选择允许查看的运行</option>
              {selections.map((s) => (
                <option value={s.selection_id} key={s.selection_id}>
                  {s.environment} · {s.instance_id} ·{" "}
                  {s.instrument_key.instrument_id}
                </option>
              ))}
            </select>
          </label>
          <span
            className={`environment env-${selection?.environment ?? "NONE"}`}
          >
            {selection?.environment ?? "未选择"}
          </span>
          <DisplayZone />
          <div className="user-menu">
            <span>{session.display_name}</span>
            <Button variant="ghost" size="sm" onClick={logout}>
              退出
            </Button>
          </div>
        </header>
        {selection && (
          <div className="binding-strip">
            <span>{selection.deployment_id}</span>
            <span>{selection.trading_run_key.run_id}</span>
            <span>对象 {selection.object_id || "不适用"}</span>
            <span>绑定 {selection.binding_version}</span>
            <span className="read-only">只读观察</span>
          </div>
        )}
        <main id="main" tabIndex={-1}>
          <Outlet />
        </main>
        <footer className="app-footer">
          Factorforge · 查询和来源时间始终为 UTC · 受理回执与实际成交分别展示 ·{" "}
          <a href="/THIRD_PARTY_NOTICES.txt" target="_blank" rel="noreferrer">
            开源许可
          </a>
        </footer>
      </div>
    </div>
  );
}
