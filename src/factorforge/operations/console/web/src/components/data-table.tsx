// Table lifecycle follows shadcn-admin's tasks-table at the frozen MIT ref.
// Removed editable/bulk-delete/sample-data paths; only supplied read DTOs render.
import { useMemo, useState } from "react";
import {
  useReactTable,
  getCoreRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  flexRender,
  type ColumnDef,
  type SortingState,
  type VisibilityState,
} from "@tanstack/react-table";
import { type Json, record } from "../api/client";
import { compare, label, scalar } from "../schemas/display";
import { Button } from "./button";
import { displayTime, useDisplay } from "../features/display-settings";
export function DataValue({ value }: { value: Json }) {
  const { zone } = useDisplay();
  if (value === null || typeof value !== "object")
    return (
      <span
        className="value"
        title={typeof value === "string" ? value : undefined}
      >
        {typeof value === "string"
          ? displayTime(scalar(value), zone)
          : scalar(value)}
      </span>
    );
  if (Array.isArray(value)) {
    if (value.length === 0) return <span>空列表</span>;
    return (
      <details>
        <summary>{value.length} 项</summary>
        {value.map((x, i) => (
          <div key={i} className="nested">
            <DataValue value={x} />
          </div>
        ))}
      </details>
    );
  }
  return (
    <details>
      <summary>{Object.keys(value).length} 个字段</summary>
      <dl className="key-values">
        {Object.entries(value).map(([k, v]) => (
          <div key={k}>
            <dt>{label(k)}</dt>
            <dd>
              <DataValue value={v} />
            </dd>
          </div>
        ))}
      </dl>
    </details>
  );
}
export function DataTable({
  data,
  onDetail,
}: {
  data: Json[];
  onDetail?: (row: Record<string, Json>) => void;
}) {
  const [sorting, setSorting] = useState<SortingState>([]);
  const [filter, setFilter] = useState("");
  const [visibility, setVisibility] = useState<VisibilityState>({});
  const records = useMemo(() => data.map((x) => record(x)), [data]);
  const keys = useMemo(
    () => Array.from(new Set(records.flatMap((x) => Object.keys(x)))),
    [records],
  );
  const columns = useMemo<ColumnDef<Record<string, Json>>[]>(
    () =>
      keys.map((k) => ({
        id: k,
        accessorFn: (row) => row[k] ?? null,
        header: label(k),
        cell: (c) => <DataValue value={c.getValue<Json>()} />,
        sortingFn: (a, b) =>
          compare(a.original[k] ?? null, b.original[k] ?? null),
        filterFn: (r, id, value) =>
          scalar(r.getValue<Json>(id))
            .toLowerCase()
            .includes(String(value).toLowerCase()),
      })),
    [keys],
  );
  const table = useReactTable({
    data: records,
    columns,
    state: { sorting, globalFilter: filter, columnVisibility: visibility },
    onSortingChange: setSorting,
    onGlobalFilterChange: setFilter,
    onColumnVisibilityChange: setVisibility,
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  });
  return (
    <>
      <div className="table-tools">
        <label>
          本页检索{" "}
          <input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="标识、状态或字段"
          />
        </label>
        <details>
          <summary>显示列</summary>
          {table.getAllLeafColumns().map((c) => (
            <label key={c.id} className="column-option">
              <input
                type="checkbox"
                checked={c.getIsVisible()}
                onChange={c.getToggleVisibilityHandler()}
              />
              {label(c.id)}
            </label>
          ))}
        </details>
        <span>{table.getFilteredRowModel().rows.length} 条已读取记录</span>
      </div>
      <div className="table-scroll">
        <table>
          <thead>
            {table.getHeaderGroups().map((g) => (
              <tr key={g.id}>
                {onDetail && <th>追溯</th>}
                {g.headers.map((h) => (
                  <th key={h.id}>
                    <button
                      className="sort-button"
                      onClick={h.column.getToggleSortingHandler()}
                    >
                      {flexRender(h.column.columnDef.header, h.getContext())}
                      {h.column.getIsSorted() === "asc"
                        ? " ↑"
                        : h.column.getIsSorted() === "desc"
                          ? " ↓"
                          : ""}
                    </button>
                  </th>
                ))}
              </tr>
            ))}
          </thead>
          <tbody>
            {table.getRowModel().rows.map((r) => (
              <tr key={r.id}>
                {onDetail && (
                  <td>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => onDetail(r.original)}
                    >
                      详情
                    </Button>
                  </td>
                )}
                {r.getVisibleCells().map((c) => (
                  <td key={c.id}>
                    {flexRender(c.column.columnDef.cell, c.getContext())}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="pagination">
        <Button
          variant="outline"
          size="sm"
          disabled={!table.getCanPreviousPage()}
          onClick={() => table.previousPage()}
        >
          本页上一组
        </Button>
        <span>
          {table.getState().pagination.pageIndex + 1} /{" "}
          {Math.max(1, table.getPageCount())}
        </span>
        <Button
          variant="outline"
          size="sm"
          disabled={!table.getCanNextPage()}
          onClick={() => table.nextPage()}
        >
          本页下一组
        </Button>
      </div>
    </>
  );
}
