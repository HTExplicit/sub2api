import type { Column, StackPart } from './types'

/** The columns drawn as lines of one host column's cell (DataTable, Column.stack). */
export interface ColumnStackSpec {
  /** the parts, in cell order: a column key, or a key with a label in front of its line (true = its column label) */
  parts: Array<string | { key: string; cellLabel?: string | true }>
  /** 'inline': the parts share one line (a row of chips) */
  layout?: 'lines' | 'inline'
  /** a line label for the host's own content (true = the host's column label) */
  cellLabel?: string | true
  /** viewport width (px) from which the parts are columns of their own again (Column.stackBelow) */
  below?: number
}

/**
 * Turns a flat column list (in the column settings' order) into the stacked list DataTable draws: every part named in
 * a spec leaves the list and is carried by its host column, and column visibility applies to both. A hidden part is
 * left out of its host's cell; a hidden host whose parts are visible keeps its place with `hostHidden`, so its cell
 * holds only their lines; a host with no visible part is a plain column. Columns without a spec pass through. Every
 * column and part keeps its position in the flat list (`order`), so a table drawn without stacking has upstream's
 * column order.
 */
export function stackColumns(
  columns: Column[],
  specs: Record<string, ColumnStackSpec>,
  isVisible: (key: string) => boolean = () => true
): Column[] {
  const byKey = new Map(columns.map((column) => [column.key, column]))
  const position = new Map(columns.map((column, index) => [column.key, index]))
  const hostOf = new Map<string, string>()
  for (const [host, spec] of Object.entries(specs)) {
    if (!byKey.has(host)) continue
    for (const part of spec.parts) hostOf.set(typeof part === 'string' ? part : part.key, host)
  }

  const result: Column[] = []
  for (const column of columns) {
    if (hostOf.has(column.key)) continue
    const spec = specs[column.key]
    const hostVisible = isVisible(column.key)
    const order = position.get(column.key)
    if (!spec) {
      if (hostVisible) result.push({ ...column, order })
      continue
    }
    const stack: StackPart[] = []
    for (const part of spec.parts) {
      const key = typeof part === 'string' ? part : part.key
      const def = byKey.get(key)
      if (!def || !isVisible(key)) continue
      stack.push({
        key: def.key,
        label: def.label,
        sortable: def.sortable,
        class: def.class,
        formatter: def.formatter,
        cellLabel: typeof part === 'string' ? undefined : part.cellLabel,
        order: position.get(key)
      })
    }
    if (stack.length === 0) {
      if (hostVisible) result.push({ ...column, order })
      continue
    }
    result.push({
      ...column,
      // a hidden host keeps its place for its parts, without a sort of its own
      sortable: hostVisible ? column.sortable : false,
      stack,
      stackLayout: spec.layout,
      cellLabel: spec.cellLabel === true ? column.label : spec.cellLabel,
      stackBelow: spec.below,
      hostHidden: hostVisible ? undefined : true,
      order
    })
  }
  return result
}

/** Every column of a stacked list with its parts as columns, in upstream's column order; hidden hosts are left out. */
export function flattenStackedColumns(columns: Column[]): Column[] {
  const result: Column[] = []
  for (const column of columns) {
    if (!column.stack?.length) {
      if (!column.hostHidden) result.push(column)
      continue
    }
    if (!column.hostHidden) result.push(plainColumn(column))
    for (const part of column.stack) result.push(plainColumn(part))
  }
  return inColumnOrder(result)
}

/** Stable sort by `order` when every column carries one (lists built by stackColumns); otherwise as given. */
export function inColumnOrder(columns: Column[]): Column[] {
  if (!columns.every((column) => typeof column.order === 'number')) return columns
  return columns
    .map((column, index) => ({ column, index }))
    .sort((a, b) => (a.column.order as number) - (b.column.order as number) || a.index - b.index)
    .map(({ column }) => column)
}

function plainColumn(column: Column | StackPart): Column {
  return {
    key: column.key,
    label: column.label,
    sortable: column.sortable,
    class: column.class,
    formatter: column.formatter,
    order: column.order
  }
}
