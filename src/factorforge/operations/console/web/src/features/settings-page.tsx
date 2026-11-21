import { useQueries } from "@tanstack/react-query";
import { Alert, Table, Tag, Tooltip, Typography } from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import { checkResponse, query, request, type Response, type Selection } from "../api/client";
import { instanceName } from "../components/shell";
import { useSession, useVisible } from "./session";
import { objectOf, panelOf } from "./use-view";
import { settingsRows, type SettingsColumn, type SettingsRow } from "./settings-model";

const sourceName: Record<string, string> = { "dynamic.status": "应用实例", run: "交易运行", account: "账户", policy: "策略参数" };

// S4-025: every instance the user may view, side by side: what it tests and
// what it is registered with. Read only; it refreshes slowly because the
// settings change only when an instance is redeployed.
export function SettingsPage() {
  const { session, selections, generation, capabilities } = useSession();
  const visible = useVisible();
  const reads = useQueries({
    queries: selections.map((s: Selection) => ({
      queryKey: ["settings", session?.user_id, s.selection_id, s.binding_version, generation],
      enabled: !!session,
      refetchInterval: visible && capabilities ? Math.max(60_000, capabilities.refresh_seconds * 1000) : (false as const),
      retry: 1,
      queryFn: async ({ signal }: { signal: AbortSignal }) => checkResponse(await request<Response>(query("/settings", s), signal), s),
    })),
  });
  const columns: SettingsColumn[] = selections.map((s, i) => {
    const panels = reads[i]?.data?.panels ?? [];
    const pick = (source: string) => {
      const p = panelOf(panels, source);
      return p && (p.state === "AVAILABLE" || p.state === "STALE") ? objectOf(p) : null;
    };
    const failed = reads[i]?.error ? Object.keys(sourceName) : panels.filter((p) => p.state === "UNAVAILABLE").map((p) => p.source);
    return { name: instanceName(s), status: pick("dynamic.status"), run: pick("run"), account: pick("account"), policy: pick("policy"), failed };
  });
  const rows = settingsRows(columns);
  const pending = reads.some((r) => r.isPending);
  const failures = columns.filter((c) => c.failed.length > 0);
  const unrecorded = columns.filter((c) => c.status && !c.status.settings).map((c) => c.name);

  const tableColumns = [
    {
      title: "设置",
      dataIndex: "label",
      key: "label",
      fixed: "left" as const,
      width: 168,
      render: (_: string, r: SettingsRow) => (
        <span className="settings-label">
          {r.label}{" "}
          <Tooltip title={r.help}>
            <QuestionCircleOutlined aria-label={r.help} className="settings-help" />
          </Tooltip>
        </span>
      ),
    },
    ...columns.map((c, i) => ({
      title: c.name,
      key: c.name,
      width: i === 0 ? 180 : 160,
      render: (_: unknown, r: SettingsRow) => (
        <span className={r.differs[i] ? "settings-diff" : undefined}>
          {r.values[i]}
          {r.differs[i] && <span className="sr-only">（与多数实例不同）</span>}
        </span>
      ),
    })),
  ];
  const groups = [...new Set(rows.map((r) => r.group))];

  return (
    <div className="page settings-page">
      <Typography.Paragraph type="secondary" className="settings-intro">
        每列是一个实例，每行是一项登记的设置。<Tag className="settings-diff-tag">标色</Tag>表示和多数实例不同的值，也就是这个实例在测试的差异。设置只读，修改要重新部署实例。
      </Typography.Paragraph>
      {failures.length > 0 && (
        <Alert
          type="warning"
          showIcon
          message={`部分数据没有读到：${failures.map((c) => `${c.name}（${c.failed.map((s) => sourceName[s] ?? s).join("、")}）`).join("；")}，对应格子显示“—”`}
        />
      )}
      {unrecorded.length > 0 && <Alert type="info" showIcon message={`${unrecorded.join("、")} 的应用实例还没有记录设置（仓位管理器升级后的第一轮会写入）`} />}
      {groups.map((g) => (
        <section key={g} className="settings-group">
          <Typography.Title level={5}>{g}</Typography.Title>
          <Table
            size="small"
            bordered
            loading={pending}
            pagination={false}
            rowKey="label"
            scroll={{ x: "max-content" }}
            columns={tableColumns}
            dataSource={rows.filter((r) => r.group === g)}
          />
        </section>
      ))}
    </div>
  );
}
