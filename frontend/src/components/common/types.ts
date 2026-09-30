/**
 * Common component types
 */

export interface Column {
  key: string
  label: string
  sortable?: boolean
  class?: string
  formatter?: (value: any, row: any) => string
  /**
   * Stacked cells (console theme): columns drawn as lines of this column's cell, in column order. Each part keeps its
   * own cell slot (`cell-<key>`), header slot (`header-<key>`) and sort control; the header lists them under this
   * column's label. Without the console theme, or from `stackBelow` up, the parts are columns of their own again.
   * Build these columns with stackColumns() (columnStack.ts).
   */
  stack?: StackPart[]
  /** 'lines' (default): one line per part; 'inline': the parts share one line (a row of chips) */
  stackLayout?: 'lines' | 'inline'
  /** a line label for this column's own content, which is then drawn as the first labelled line of the cell */
  cellLabel?: string
  /** viewport width (px) from which the parts are columns of their own; unset = always stacked */
  stackBelow?: number
  /** the column itself is hidden by the column settings: its cell holds only the lines of its stacked parts */
  hostHidden?: boolean
  /** position in the flat column list (set by stackColumns): the parts drawn as columns take upstream's order back */
  order?: number
}

export interface StackPart {
  key: string
  label: string
  sortable?: boolean
  class?: string
  formatter?: (value: any, row: any) => string
  /** a label in front of the part's line: a text, or true for the part's own column label */
  cellLabel?: string | true
  /** position in the flat column list (see Column.order) */
  order?: number
}
