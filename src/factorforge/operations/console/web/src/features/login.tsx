import { useState, type FormEvent } from "react";
import { ApiError, request, type Session } from "../api/client";
import { Alert, Button, Card, Input, Space, Typography } from "antd";
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
      <Card className="login-card">
        <form onSubmit={submit}>
          <Space direction="vertical" size={14} style={{ width: "100%" }}>
            <div className="login-brand">
              <span className="brand-symbol">F</span> Factorforge
            </div>
            <Typography.Text type="secondary">登录后查看各实例的运行状态与交易。</Typography.Text>
            <label>
              用户名
              <Input name="username" autoComplete="username" required autoFocus />
            </label>
            <label>
              密码
              <Input.Password name="password" autoComplete="current-password" required />
            </label>
            {error && <Alert role="alert" type="error" showIcon message={error} />}
            <Button type="primary" htmlType="submit" block loading={busy}>
              {busy ? "正在验证…" : "进入管理台"}
            </Button>
          </Space>
        </form>
      </Card>
    </main>
  );
}
