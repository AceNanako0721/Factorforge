import { useMemo, useState } from "react";
import { Background, Handle, MarkerType, Position, ReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Alert, Badge, Button, Descriptions, Drawer, Empty, Space, Table, Tag, Tooltip, Typography } from "antd";
import { record, type Json, type Selection } from "../api/client";
import { poolView } from "./confidence";
import { shortTime, useDisplay } from "./display-settings";
import { FLOW_EDGES, flowNodes, type FlowNode, type Health } from "./flow-model";
import { directionText, dynamicRange, text } from "./market-model";
import { useSession } from "./session";
import { useTheme } from "./theme";
import { listOf, objectOf, panelOf, useProtections, useView } from "./use-view";

const healthColor: Record<Health, string> = { ok: "#2e8b57", warn: "#d08a1d", error: "#c4553f", idle: "#8c8c8c" };
const healthText: Record<Health, string> = { ok: "正常", warn: "注意", error: "异常", idle: "未启用" };
const badgeStatus: Record<Health, "success" | "warning" | "error" | "default"> = { ok: "success", warn: "warning", error: "error", idle: "default" };

// Two rows read left to right, then right to left: news to decision on top,
// executor to account below; the stop module feeds the executor.
const place: Record<string, [number, number]> = {
  news: [0, 0],
  judgment: [270, 0],
  chain: [540, 0],
  pool: [810, 0],
  decision: [1080, 0],
  stops: [1350, 190],
  executor: [1080, 190],
  venue: [810, 190],
  account: [540, 190],
};

// Panels this page draws; others in the same views do not raise the notice.
const USED = new Set(["dynamic.status", "dynamic.judgments", "account", "positions", "run", "object", "pool", "decisions", "trading.operations"]);

type Data = { node: FlowNode; selected: boolean };
function ModuleNode({ data }: NodeProps<Node<Data>>) {
  const { node, selected } = data;
  const reverse = ["executor", "venue", "account"].includes(node.id);
  return (
    <div className={`flow-node health-${node.health}${selected ? " selected" : ""}`} style={{ borderColor: healthColor[node.health] }}>
      <Handle type="target" position={node.id === "executor" ? Position.Top : reverse ? Position.Right : Position.Left} />
      {node.id === "executor" && <Handle id="side" type="target" position={Position.Right} />}
      <div className="flow-node-head">
        <span className={`pulse pulse-${node.health}`} />
        <b>{node.title}</b>
        <span className="flow-node-state" style={{ color: healthColor[node.health] }}>
          {healthText[node.health]}
        </span>
      </div>
      <div className="flow-node-line">{node.headline}</div>
      <Handle type="source" position={node.id === "decision" ? Position.Bottom : reverse || node.id === "stops" ? Position.Left : Position.Right} />
    </div>
  );
}
const nodeTypes = { module: ModuleNode };

export function FlowPage() {
  const { selection } = useSession();
  const s = selection as Selection;
  const { zone } = useDisplay();
  const { mode } = useTheme();
  const t = (v: Json) => (typeof v === "string" ? shortTime(v, zone) : "—");
  const dynamic = useView("dynamic", () => dynamicRange());
  const overview = useView("overview");
  const operations = useView("operations");
  const learning = useView("learning");
  const [open, setOpen] = useState<string | null>(null);

  const status = objectOf(panelOf(dynamic.panels, "dynamic.status"));
  const decisions = listOf(panelOf(overview.panels, "decisions")).sort((a, b) =>
    String(b.available_cutoff).localeCompare(String(a.available_cutoff)),
  );
  const pool = poolView(objectOf(panelOf(overview.panels, "pool")));
  const parameters = listOf(panelOf(learning.panels, "parameters"));
  const object = objectOf(panelOf(overview.panels, "object"));
  const judgments = listOf(panelOf(dynamic.panels, "dynamic.judgments"));
  const protections = useProtections(listOf(panelOf(dynamic.panels, "protections")));
  const nodes = flowNodes({
    now: Date.now(),
    status,
    judgments,
    account: objectOf(panelOf(dynamic.panels, "account")),
    position: listOf(panelOf(dynamic.panels, "positions")).find(
      (p) => record(p.instrument_key).instrument_id === s.instrument_key.instrument_id && p.owner_id === s.owner_id,
    ),
    protections,
    object,
    run: objectOf(panelOf(overview.panels, "run")),
    operations: objectOf(panelOf(operations.panels, "trading.operations")),
    decision: decisions[0],
    pool: { net: pool.net, segments: pool.segments.length },
    parameters: parameters.find((p) => p.version === object.parameter_version) ?? parameters[0],
    time: t,
  });
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const flow: Node<Data>[] = nodes.map((n) => ({
    id: n.id,
    type: "module",
    position: { x: place[n.id][0], y: place[n.id][1] },
    data: { node: n, selected: open === n.id },
  }));
  const edges: Edge[] = useMemo(
    () =>
      FLOW_EDGES.map(([a, b]) => {
        const h = byId.get(a)?.health ?? "idle";
        return {
          id: `${a}-${b}`,
          source: a,
          target: b,
          targetHandle: a === "stops" ? "side" : undefined,
          animated: h === "ok",
          style: { stroke: healthColor[h], strokeWidth: 2 },
          markerEnd: { type: MarkerType.ArrowClosed, color: healthColor[h] },
        };
      }),
    [nodes.map((n) => n.health).join()],
  );
  const current = open ? byId.get(open) : undefined;
  const loading = dynamic.pending || overview.pending;
  const problems = nodes.filter((n) => n.health === "error" || n.health === "warn");

  return (
    <div className="page">
      <Space className="flow-summary" wrap>
        {loading ? (
          <Typography.Text type="secondary">正在读取各模块状态…</Typography.Text>
        ) : problems.length === 0 ? (
          <Alert type="success" showIcon message="所有模块运行正常" />
        ) : (
          <Alert type={problems.some((p) => p.health === "error") ? "error" : "warning"} showIcon message={problems.map((p) => `${p.title}：${p.headline}`).join("；")} />
        )}
        {(dynamic.error || overview.error) && <Alert type="warning" showIcon message="本次刷新失败，显示的是上一次成功读取的数据" />}
        {!dynamic.error && [...dynamic.panels, ...overview.panels, ...operations.panels].some((p) => USED.has(p.source) && (p.state === "STALE" || p.state === "UNAVAILABLE")) && (
          <Alert type="warning" showIcon message="部分数据暂时读不到，显示的是上一次读取的结果" />
        )}
      </Space>
      <div className="flow-canvas" aria-label="实时流程图">
        <ReactFlow
          nodes={flow}
          edges={edges}
          nodeTypes={nodeTypes}
          colorMode={mode}
          fitView
          fitViewOptions={{ padding: 0.08, maxZoom: 1.25 }}
          nodesDraggable={false}
          nodesConnectable={false}
          elementsSelectable={false}
          proOptions={{ hideAttribution: false }}
          onNodeClick={(_, n) => setOpen(n.id)}
          minZoom={0.3}
        >
          <Background gap={24} />
        </ReactFlow>
      </div>
      <Typography.Text type="secondary" className="small">
        点击模块查看详情和参数，每 15 秒自动刷新。
      </Typography.Text>
      <Drawer
        open={!!current}
        onClose={() => setOpen(null)}
        width={460}
        title={
          current && (
            <Space>
              <Badge status={badgeStatus[current.health]} />
              {current.title}
              <Tag color={healthColor[current.health]}>{healthText[current.health]}</Tag>
            </Space>
          )
        }
      >
        {current && (
          <Space direction="vertical" size={16} style={{ width: "100%" }}>
            <Typography.Paragraph strong>{current.headline}</Typography.Paragraph>
            {current.rows.length > 0 ? (
              <Descriptions size="small" column={1} bordered items={current.rows.map((r, i) => ({ key: i, label: r.label, children: r.value }))} />
            ) : (
              <Empty description="没有更多状态" image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
            {current.id === "judgment" && <RecentJudgments judgments={judgments} t={t} />}
            {current.params.length > 0 && (
              <div>
                <Space className="params-head">
                  <Typography.Title level={5} style={{ margin: 0 }}>
                    参数
                  </Typography.Title>
                  <Tooltip title="管理台只读。参数修改需要经过策略参数发布流程，暂不在这里修改。">
                    <Button size="small" disabled>
                      修改
                    </Button>
                  </Tooltip>
                </Space>
                <Table
                  size="small"
                  pagination={false}
                  rowKey="name"
                  dataSource={current.params}
                  columns={[
                    { title: "参数", dataIndex: "name" },
                    { title: "当前值", dataIndex: "value", align: "right" },
                    { title: "说明", dataIndex: "note", render: (v) => <Typography.Text type="secondary">{v}</Typography.Text> },
                  ]}
                />
              </div>
            )}
          </Space>
        )}
      </Drawer>
    </div>
  );
}

function RecentJudgments({ judgments, t }: { judgments: Record<string, Json>[]; t: (v: Json) => string }) {
  const recent = [...judgments].sort((a, b) => String(b.at).localeCompare(String(a.at))).slice(0, 6);
  return (
    <div>
      <Typography.Title level={5}>最近研判</Typography.Title>
      <ul className="recent">
        {recent.map((j) => (
          <li key={String(j.judgment_id)}>
            <Tag color={j.ok !== true ? "default" : j.direction === "SHORT" ? "red" : j.direction === "LONG" ? "green" : "blue"}>
              {j.ok === true ? `${directionText[String(j.direction)] ?? j.direction} ${j.confidence}` : "失败"}
            </Tag>
            <Typography.Text type="secondary">{t(j.at)}</Typography.Text>
            <div className="recent-text">{text(j.thesis) ?? text(j.reason) ?? ""}</div>
          </li>
        ))}
      </ul>
    </div>
  );
}
