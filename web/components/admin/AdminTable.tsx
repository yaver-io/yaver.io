// Dense, sortable, sticky-header admin table. Tabular fields render
// in mono so columns align visually.
"use client";

import React, { useMemo, useState } from "react";
import { ArrowDown, ArrowUp } from "./icons";

export type Column<T> = {
  key: string;
  header: string;
  /** Optional sort function. If absent, the column is presentational only. */
  sort?: (a: T, b: T) => number;
  /** className applied to header AND data cells (alignment, width, etc.) */
  cellClass?: string;
  render: (row: T) => React.ReactNode;
};

export function AdminTable<T>({
  rows,
  columns,
  defaultSortKey,
  defaultSortDir = "desc",
  total,
  rowKey,
  emptyState,
}: {
  rows: T[];
  columns: Column<T>[];
  defaultSortKey?: string;
  defaultSortDir?: "asc" | "desc";
  total?: number;
  rowKey: (row: T, idx: number) => string;
  emptyState?: React.ReactNode;
}) {
  const [sortKey, setSortKey] = useState<string | null>(defaultSortKey ?? null);
  const [sortDir, setSortDir] = useState<"asc" | "desc">(defaultSortDir);

  const sortedRows = useMemo(() => {
    if (!sortKey) return rows;
    const col = columns.find((c) => c.key === sortKey);
    if (!col?.sort) return rows;
    const sgn = sortDir === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => sgn * col.sort!(a, b));
  }, [rows, columns, sortKey, sortDir]);

  function onHeaderClick(col: Column<T>) {
    if (!col.sort) return;
    if (sortKey === col.key) {
      setSortDir(sortDir === "asc" ? "desc" : "asc");
    } else {
      setSortKey(col.key);
      setSortDir("desc");
    }
  }

  return (
    <div className="overflow-hidden rounded-md border border-surface-800 bg-surface-900">
      <div className="flex items-center justify-between border-b border-surface-800 bg-surface-950/40 px-4 py-2 text-[11px] text-surface-400">
        <span className="font-mono uppercase tracking-wider">
          Showing {rows.length}
          {typeof total === "number" && total !== rows.length ? ` of ${total}` : ""} (org-wide)
        </span>
      </div>
      {rows.length === 0 ? (
        <div className="p-6">{emptyState ?? <span className="text-[12px] text-surface-400">No rows.</span>}</div>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead className="sticky top-0 z-10 bg-surface-900/95 backdrop-blur">
              <tr>
                {columns.map((col) => {
                  const isSorted = sortKey === col.key;
                  return (
                    <th
                      key={col.key}
                      onClick={() => onHeaderClick(col)}
                      className={`select-none border-b border-surface-800 px-3 py-2 text-left text-[11px] font-medium uppercase tracking-wider text-surface-400 ${
                        col.sort ? "cursor-pointer hover:text-surface-100" : ""
                      } ${col.cellClass ?? ""}`}
                    >
                      <span className="inline-flex items-center gap-1">
                        {col.header}
                        {col.sort && isSorted && (sortDir === "asc" ? (
                          <ArrowUp className="h-3 w-3" />
                        ) : (
                          <ArrowDown className="h-3 w-3" />
                        ))}
                      </span>
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody>
              {sortedRows.map((row, idx) => (
                <tr
                  key={rowKey(row, idx)}
                  className="border-b border-surface-850 last:border-0 hover:bg-surface-850/60"
                >
                  {columns.map((col) => (
                    <td
                      key={col.key}
                      className={`px-3 py-2 align-middle text-surface-200 ${col.cellClass ?? ""}`}
                    >
                      {col.render(row)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
