import { useState, type FormEvent } from "react";
import { ApiError, request, type Session } from "../api/client";
import { Button } from "../components/button";
import { useSession } from "./session";
export function Login() {
  const { setSession } = useSession();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const form = e.currentTarget;
    const data = new FormData(form);
    try {
      const challenge = await request<{ csrf: string; expires_at: string }>(
        "/session/challenge",
      );
      const session = await request<Session>("/session", undefined, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          username: data.get("username"),
          password: data.get("password"),
          csrf: challenge.csrf,
        }),
      });
      form.reset();
      setSession(session);
    } catch (e) {
      setError(e instanceof ApiError ? e.code : "连接暂时不可用");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login-page">
      <div className="login-brand">
        <span className="brand-symbol">F</span> FACTORFORGE
      </div>
      <form className="login-card" onSubmit={submit}>
        <p className="eyebrow">LOCAL OBSERVATORY</p>
        <h1>观察系统，追溯每个判断。</h1>
        <p className="muted">登录后查看获准的交易运行和观测对象。</p>
        <label>
          用户名
          <input name="username" autoComplete="username" required autoFocus />
        </label>
        <label>
          密码
          <input
            name="password"
            type="password"
            autoComplete="current-password"
            required
          />
        </label>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
        <Button type="submit" disabled={busy}>
          {busy ? "正在验证…" : "进入管理台"}
        </Button>
        <p className="muted small">
          管理台提供只读观察。SIM / LIVE 的运行范围会在页面中持续显示。
        </p>
      </form>
      <div className="login-footer">事实 · 判断 · 执行，各自保留来源</div>
    </main>
  );
}
