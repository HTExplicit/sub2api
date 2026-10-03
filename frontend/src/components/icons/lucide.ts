/*
 * Lucide glyphs for Icon.vue while the console theme is on (<html class="flat-theme">, utils/flatTheme.ts).
 * With the theme off Icon.vue keeps its Heroicons paths.
 *
 * Source: npm lucide-static 0.469.0 (sha512-ravTgZodIVLO53rJyjIq0iuCRHs+kjd3dAfOcTbA41KCDfdy0Pt6Me8akkhda3jRpbIxjKe1PpYZOg2eQnI/lA==), file icon-nodes.json.
 * Every `nodes` entry is a verbatim copy of that file's entry. The release is pinned on purpose: all Lucide icons
 * bundled by the reference console (platform.experientiallabs.ai) match it, while lucide 1.x redrew a third of them.
 * To add an Icon.vue name: copy its glyph from that file into `nodes` and map the name in `lucideIcons`
 * (vue-tsc fails while an Icon.vue name has no glyph here).
 *
 * @license lucide-static v0.469.0 - ISC (portions derived from Feather: MIT, Cole Bemis; see THIRD_PARTY_NOTICES.md)
 *
 * ISC License
 *
 * Copyright (c) for portions of Lucide are held by Cole Bemis 2013-2022 as part of Feather (MIT). All other copyright (c) for Lucide are held by Lucide Contributors 2022.
 *
 * Permission to use, copy, modify, and/or distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */
import { h, type FunctionalComponent } from 'vue'

type LucideShape = 'circle' | 'ellipse' | 'line' | 'path' | 'polygon' | 'polyline' | 'rect'
export type LucideIconNode = readonly (readonly [LucideShape, Readonly<Record<string, string>>])[]
export interface LucideGlyph {
  name: string
  nodes: LucideIconNode
}

const nodes = {
  'arrow-down': [['path', { d: 'M12 5v14' }], ['path', { d: 'm19 12-7 7-7-7' }]],
  'arrow-left': [['path', { d: 'm12 19-7-7 7-7' }], ['path', { d: 'M19 12H5' }]],
  'arrow-right': [['path', { d: 'M5 12h14' }], ['path', { d: 'm12 5 7 7-7 7' }]],
  'arrow-right-left': [['path', { d: 'm16 3 4 4-4 4' }], ['path', { d: 'M20 7H4' }], ['path', { d: 'm8 21-4-4 4-4' }], ['path', { d: 'M4 17h16' }]],
  'arrow-up': [['path', { d: 'm5 12 7-7 7 7' }], ['path', { d: 'M12 19V5' }]],
  'arrow-up-down': [['path', { d: 'm21 16-4 4-4-4' }], ['path', { d: 'M17 20V4' }], ['path', { d: 'm3 8 4-4 4 4' }], ['path', { d: 'M7 4v16' }]],
  'badge-check': [['path', { d: 'M3.85 8.62a4 4 0 0 1 4.78-4.77 4 4 0 0 1 6.74 0 4 4 0 0 1 4.78 4.78 4 4 0 0 1 0 6.74 4 4 0 0 1-4.77 4.78 4 4 0 0 1-6.75 0 4 4 0 0 1-4.78-4.77 4 4 0 0 1 0-6.76Z' }], ['path', { d: 'm9 12 2 2 4-4' }]],
  'ban': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'm4.9 4.9 14.2 14.2' }]],
  'bell': [['path', { d: 'M10.268 21a2 2 0 0 0 3.464 0' }], ['path', { d: 'M3.262 15.326A1 1 0 0 0 4 17h16a1 1 0 0 0 .74-1.673C19.41 13.956 18 12.499 18 8A6 6 0 0 0 6 8c0 4.499-1.411 5.956-2.738 7.326' }]],
  'book-open': [['path', { d: 'M12 7v14' }], ['path', { d: 'M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z' }]],
  'box': [['path', { d: 'M21 8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z' }], ['path', { d: 'm3.3 7 8.7 5 8.7-5' }], ['path', { d: 'M12 22V12' }]],
  'brain': [['path', { d: 'M12 5a3 3 0 1 0-5.997.125 4 4 0 0 0-2.526 5.77 4 4 0 0 0 .556 6.588A4 4 0 1 0 12 18Z' }], ['path', { d: 'M12 5a3 3 0 1 1 5.997.125 4 4 0 0 1 2.526 5.77 4 4 0 0 1-.556 6.588A4 4 0 1 1 12 18Z' }], ['path', { d: 'M15 13a4.5 4.5 0 0 1-3-4 4.5 4.5 0 0 1-3 4' }], ['path', { d: 'M17.599 6.5a3 3 0 0 0 .399-1.375' }], ['path', { d: 'M6.003 5.125A3 3 0 0 0 6.401 6.5' }], ['path', { d: 'M3.477 10.896a4 4 0 0 1 .585-.396' }], ['path', { d: 'M19.938 10.5a4 4 0 0 1 .585.396' }], ['path', { d: 'M6 18a4 4 0 0 1-1.967-.516' }], ['path', { d: 'M19.967 17.484A4 4 0 0 1 18 18' }]],
  'calculator': [['rect', { width: '16', height: '20', x: '4', y: '2', rx: '2' }], ['line', { x1: '8', x2: '16', y1: '6', y2: '6' }], ['line', { x1: '16', x2: '16', y1: '14', y2: '18' }], ['path', { d: 'M16 10h.01' }], ['path', { d: 'M12 10h.01' }], ['path', { d: 'M8 10h.01' }], ['path', { d: 'M12 14h.01' }], ['path', { d: 'M8 14h.01' }], ['path', { d: 'M12 18h.01' }], ['path', { d: 'M8 18h.01' }]],
  'calendar': [['path', { d: 'M8 2v4' }], ['path', { d: 'M16 2v4' }], ['rect', { width: '18', height: '18', x: '3', y: '4', rx: '2' }], ['path', { d: 'M3 10h18' }]],
  'chart-column': [['path', { d: 'M3 3v16a2 2 0 0 0 2 2h16' }], ['path', { d: 'M18 17V9' }], ['path', { d: 'M13 17V5' }], ['path', { d: 'M8 17v-3' }]],
  'check': [['path', { d: 'M20 6 9 17l-5-5' }]],
  'chevron-down': [['path', { d: 'm6 9 6 6 6-6' }]],
  'chevron-left': [['path', { d: 'm15 18-6-6 6-6' }]],
  'chevron-right': [['path', { d: 'm9 18 6-6-6-6' }]],
  'chevron-up': [['path', { d: 'm18 15-6-6-6 6' }]],
  'chevrons-up-down': [['path', { d: 'm7 15 5 5 5-5' }], ['path', { d: 'm7 9 5-5 5 5' }]],
  'circle-alert': [['circle', { cx: '12', cy: '12', r: '10' }], ['line', { x1: '12', x2: '12', y1: '8', y2: '12' }], ['line', { x1: '12', x2: '12.01', y1: '16', y2: '16' }]],
  'circle-check': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'm9 12 2 2 4-4' }]],
  'circle-dollar-sign': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M16 8h-6a2 2 0 1 0 0 4h4a2 2 0 1 1 0 4H8' }], ['path', { d: 'M12 18V6' }]],
  'circle-help': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3' }], ['path', { d: 'M12 17h.01' }]],
  'circle-user': [['circle', { cx: '12', cy: '12', r: '10' }], ['circle', { cx: '12', cy: '10', r: '3' }], ['path', { d: 'M7 20.662V19a2 2 0 0 1 2-2h6a2 2 0 0 1 2 2v1.662' }]],
  'circle-x': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'm15 9-6 6' }], ['path', { d: 'm9 9 6 6' }]],
  'clipboard': [['rect', { width: '8', height: '4', x: '8', y: '2', rx: '1', ry: '1' }], ['path', { d: 'M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2' }]],
  'clock': [['circle', { cx: '12', cy: '12', r: '10' }], ['polyline', { points: '12 6 12 12 16 14' }]],
  'cloud': [['path', { d: 'M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z' }]],
  'copy': [['rect', { width: '14', height: '14', x: '8', y: '8', rx: '2', ry: '2' }], ['path', { d: 'M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2' }]],
  'cpu': [['rect', { width: '16', height: '16', x: '4', y: '4', rx: '2' }], ['rect', { width: '6', height: '6', x: '9', y: '9', rx: '1' }], ['path', { d: 'M15 2v2' }], ['path', { d: 'M15 20v2' }], ['path', { d: 'M2 15h2' }], ['path', { d: 'M2 9h2' }], ['path', { d: 'M20 15h2' }], ['path', { d: 'M20 9h2' }], ['path', { d: 'M9 2v2' }], ['path', { d: 'M9 20v2' }]],
  'credit-card': [['rect', { width: '20', height: '14', x: '2', y: '5', rx: '2' }], ['line', { x1: '2', x2: '22', y1: '10', y2: '10' }]],
  'database': [['ellipse', { cx: '12', cy: '5', rx: '9', ry: '3' }], ['path', { d: 'M3 5V19A9 3 0 0 0 21 19V5' }], ['path', { d: 'M3 12A9 3 0 0 0 21 12' }]],
  'download': [['path', { d: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4' }], ['polyline', { points: '7 10 12 15 17 10' }], ['line', { x1: '12', x2: '12', y1: '15', y2: '3' }]],
  'ellipsis': [['circle', { cx: '12', cy: '12', r: '1' }], ['circle', { cx: '19', cy: '12', r: '1' }], ['circle', { cx: '5', cy: '12', r: '1' }]],
  'external-link': [['path', { d: 'M15 3h6v6' }], ['path', { d: 'M10 14 21 3' }], ['path', { d: 'M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6' }]],
  'eye': [['path', { d: 'M2.062 12.348a1 1 0 0 1 0-.696 10.75 10.75 0 0 1 19.876 0 1 1 0 0 1 0 .696 10.75 10.75 0 0 1-19.876 0' }], ['circle', { cx: '12', cy: '12', r: '3' }]],
  'eye-off': [['path', { d: 'M10.733 5.076a10.744 10.744 0 0 1 11.205 6.575 1 1 0 0 1 0 .696 10.747 10.747 0 0 1-1.444 2.49' }], ['path', { d: 'M14.084 14.158a3 3 0 0 1-4.242-4.242' }], ['path', { d: 'M17.479 17.499a10.75 10.75 0 0 1-15.417-5.151 1 1 0 0 1 0-.696 10.75 10.75 0 0 1 4.446-5.143' }], ['path', { d: 'm2 2 20 20' }]],
  'file-text': [['path', { d: 'M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z' }], ['path', { d: 'M14 2v4a2 2 0 0 0 2 2h4' }], ['path', { d: 'M10 9H8' }], ['path', { d: 'M16 13H8' }], ['path', { d: 'M16 17H8' }]],
  'filter': [['polygon', { points: '22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3' }]],
  'flame': [['path', { d: 'M8.5 14.5A2.5 2.5 0 0 0 11 12c0-1.38-.5-2-1-3-1.072-2.143-.224-4.054 2-6 .5 2.5 2 4.9 4 6.5 2 1.6 3 3.5 3 5.5a7 7 0 1 1-14 0c0-1.153.433-2.294 1-3a2.5 2.5 0 0 0 2.5 2.5z' }]],
  'flask-conical': [['path', { d: 'M14 2v6a2 2 0 0 0 .245.96l5.51 10.08A2 2 0 0 1 18 22H6a2 2 0 0 1-1.755-2.96l5.51-10.08A2 2 0 0 0 10 8V2' }], ['path', { d: 'M6.453 15h11.094' }], ['path', { d: 'M8.5 2h7' }]],
  'gift': [['rect', { x: '3', y: '8', width: '18', height: '4', rx: '1' }], ['path', { d: 'M12 8v13' }], ['path', { d: 'M19 12v7a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-7' }], ['path', { d: 'M7.5 8a2.5 2.5 0 0 1 0-5A4.8 8 0 0 1 12 8a4.8 8 0 0 1 4.5-5 2.5 2.5 0 0 1 0 5' }]],
  'globe': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20' }], ['path', { d: 'M2 12h20' }]],
  'house': [['path', { d: 'M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8' }], ['path', { d: 'M3 10a2 2 0 0 1 .709-1.528l7-5.999a2 2 0 0 1 2.582 0l7 5.999A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z' }]],
  'inbox': [['polyline', { points: '22 12 16 12 14 15 10 15 8 12 2 12' }], ['path', { d: 'M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z' }]],
  'info': [['circle', { cx: '12', cy: '12', r: '10' }], ['path', { d: 'M12 16v-4' }], ['path', { d: 'M12 8h.01' }]],
  'key-round': [['path', { d: 'M2.586 17.414A2 2 0 0 0 2 18.828V21a1 1 0 0 0 1 1h3a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h1a1 1 0 0 0 1-1v-1a1 1 0 0 1 1-1h.172a2 2 0 0 0 1.414-.586l.814-.814a6.5 6.5 0 1 0-4-4z' }], ['circle', { cx: '16.5', cy: '7.5', r: '.5', fill: 'currentColor' }]],
  'layout-grid': [['rect', { width: '7', height: '7', x: '3', y: '3', rx: '1' }], ['rect', { width: '7', height: '7', x: '14', y: '3', rx: '1' }], ['rect', { width: '7', height: '7', x: '14', y: '14', rx: '1' }], ['rect', { width: '7', height: '7', x: '3', y: '14', rx: '1' }]],
  'lightbulb': [['path', { d: 'M15 14c.2-1 .7-1.7 1.5-2.5 1-.9 1.5-2.2 1.5-3.5A6 6 0 0 0 6 8c0 1 .2 2.2 1.5 3.5.7.7 1.3 1.5 1.5 2.5' }], ['path', { d: 'M9 18h6' }], ['path', { d: 'M10 22h4' }]],
  'link': [['path', { d: 'M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71' }], ['path', { d: 'M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71' }]],
  'lock': [['rect', { width: '18', height: '11', x: '3', y: '11', rx: '2', ry: '2' }], ['path', { d: 'M7 11V7a5 5 0 0 1 10 0v4' }]],
  'log-in': [['path', { d: 'M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4' }], ['polyline', { points: '10 17 15 12 10 7' }], ['line', { x1: '15', x2: '3', y1: '12', y2: '12' }]],
  'mail': [['rect', { width: '20', height: '16', x: '2', y: '4', rx: '2' }], ['path', { d: 'm22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7' }]],
  'menu': [['line', { x1: '4', x2: '20', y1: '12', y2: '12' }], ['line', { x1: '4', x2: '20', y1: '6', y2: '6' }], ['line', { x1: '4', x2: '20', y1: '18', y2: '18' }]],
  'message-circle-more': [['path', { d: 'M7.9 20A9 9 0 1 0 4 16.1L2 22Z' }], ['path', { d: 'M8 12h.01' }], ['path', { d: 'M12 12h.01' }], ['path', { d: 'M16 12h.01' }]],
  'message-square-more': [['path', { d: 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z' }], ['path', { d: 'M8 10h.01' }], ['path', { d: 'M12 10h.01' }], ['path', { d: 'M16 10h.01' }]],
  'moon': [['path', { d: 'M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z' }]],
  'play': [['polygon', { points: '6 3 20 12 6 21 6 3' }]],
  'plus': [['path', { d: 'M5 12h14' }], ['path', { d: 'M12 5v14' }]],
  'refresh-cw': [['path', { d: 'M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8' }], ['path', { d: 'M21 3v5h-5' }], ['path', { d: 'M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16' }], ['path', { d: 'M8 16H3v5' }]],
  'rotate-cw': [['path', { d: 'M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8' }], ['path', { d: 'M21 3v5h-5' }]],
  'search': [['circle', { cx: '11', cy: '11', r: '8' }], ['path', { d: 'm21 21-4.3-4.3' }]],
  'server': [['rect', { width: '20', height: '8', x: '2', y: '2', rx: '2', ry: '2' }], ['rect', { width: '20', height: '8', x: '2', y: '14', rx: '2', ry: '2' }], ['line', { x1: '6', x2: '6.01', y1: '6', y2: '6' }], ['line', { x1: '6', x2: '6.01', y1: '18', y2: '18' }]],
  'settings': [['path', { d: 'M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z' }], ['circle', { cx: '12', cy: '12', r: '3' }]],
  'shield-check': [['path', { d: 'M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z' }], ['path', { d: 'm9 12 2 2 4-4' }]],
  'sparkles': [['path', { d: 'M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.581a.5.5 0 0 1 0 .964L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z' }], ['path', { d: 'M20 3v4' }], ['path', { d: 'M22 5h-4' }], ['path', { d: 'M4 17v2' }], ['path', { d: 'M5 18H3' }]],
  'square-pen': [['path', { d: 'M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7' }], ['path', { d: 'M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z' }]],
  'square-terminal': [['path', { d: 'm7 11 2-2-2-2' }], ['path', { d: 'M11 13h4' }], ['rect', { width: '18', height: '18', x: '3', y: '3', rx: '2', ry: '2' }]],
  'sun': [['circle', { cx: '12', cy: '12', r: '4' }], ['path', { d: 'M12 2v2' }], ['path', { d: 'M12 20v2' }], ['path', { d: 'm4.93 4.93 1.41 1.41' }], ['path', { d: 'm17.66 17.66 1.41 1.41' }], ['path', { d: 'M2 12h2' }], ['path', { d: 'M20 12h2' }], ['path', { d: 'm6.34 17.66-1.41 1.41' }], ['path', { d: 'm19.07 4.93-1.41 1.41' }]],
  'trash-2': [['path', { d: 'M3 6h18' }], ['path', { d: 'M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6' }], ['path', { d: 'M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2' }], ['line', { x1: '10', x2: '10', y1: '11', y2: '17' }], ['line', { x1: '14', x2: '14', y1: '11', y2: '17' }]],
  'trending-up': [['polyline', { points: '22 7 13.5 15.5 8.5 10.5 2 17' }], ['polyline', { points: '16 7 22 7 22 13' }]],
  'triangle-alert': [['path', { d: 'm21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3' }], ['path', { d: 'M12 9v4' }], ['path', { d: 'M12 17h.01' }]],
  'trophy': [['path', { d: 'M6 9H4.5a2.5 2.5 0 0 1 0-5H6' }], ['path', { d: 'M18 9h1.5a2.5 2.5 0 0 0 0-5H18' }], ['path', { d: 'M4 22h16' }], ['path', { d: 'M10 14.66V17c0 .55-.47.98-.97 1.21C7.85 18.75 7 20.24 7 22' }], ['path', { d: 'M14 14.66V17c0 .55.47.98.97 1.21C16.15 18.75 17 20.24 17 22' }], ['path', { d: 'M18 2H6v7a6 6 0 0 0 12 0V2Z' }]],
  'upload': [['path', { d: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4' }], ['polyline', { points: '17 8 12 3 7 8' }], ['line', { x1: '12', x2: '12', y1: '3', y2: '15' }]],
  'user': [['path', { d: 'M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2' }], ['circle', { cx: '12', cy: '7', r: '4' }]],
  'user-plus': [['path', { d: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2' }], ['circle', { cx: '9', cy: '7', r: '4' }], ['line', { x1: '19', x2: '19', y1: '8', y2: '14' }], ['line', { x1: '22', x2: '16', y1: '11', y2: '11' }]],
  'users': [['path', { d: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2' }], ['circle', { cx: '9', cy: '7', r: '4' }], ['path', { d: 'M22 21v-2a4 4 0 0 0-3-3.87' }], ['path', { d: 'M16 3.13a4 4 0 0 1 0 7.75' }]],
  'x': [['path', { d: 'M18 6 6 18' }], ['path', { d: 'm6 6 12 12' }]],
  'zap': [['path', { d: 'M4 14a1 1 0 0 1-.78-1.63l9.9-10.2a.5.5 0 0 1 .86.46l-1.92 6.02A1 1 0 0 0 13 10h7a1 1 0 0 1 .78 1.63l-9.9 10.2a.5.5 0 0 1-.86-.46l1.92-6.02A1 1 0 0 0 11 14z' }]],
  // layout chrome (AppSidebar / AppHeader)
  'layout-dashboard': [['rect', { width: '7', height: '9', x: '3', y: '3', rx: '1' }], ['rect', { width: '7', height: '5', x: '14', y: '3', rx: '1' }], ['rect', { width: '7', height: '9', x: '14', y: '12', rx: '1' }], ['rect', { width: '7', height: '5', x: '3', y: '16', rx: '1' }]],
  'images': [['path', { d: 'M18 22H4a2 2 0 0 1-2-2V6' }], ['path', { d: 'm22 13-1.296-1.296a2.41 2.41 0 0 0-3.408 0L11 18' }], ['circle', { cx: '12', cy: '8', r: '2' }], ['rect', { width: '16', height: '16', x: '6', y: '2', rx: '2' }]],
  'folder': [['path', { d: 'M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z' }]],
  'layers': [['path', { d: 'M12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83z' }], ['path', { d: 'M2 12a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 12' }], ['path', { d: 'M2 17a1 1 0 0 0 .58.91l8.6 3.91a2 2 0 0 0 1.65 0l8.58-3.9A1 1 0 0 0 22 17' }]],
  'coins': [['circle', { cx: '8', cy: '8', r: '6' }], ['path', { d: 'M18.09 10.37A6 6 0 1 1 10.34 18' }], ['path', { d: 'M7 6h1v4' }], ['path', { d: 'm16.71 13.88.7.71-2.82 2.82' }]],
  'ticket': [['path', { d: 'M2 9a3 3 0 0 1 0 6v2a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-2a3 3 0 0 1 0-6V7a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2Z' }], ['path', { d: 'M13 5v2' }], ['path', { d: 'M13 17v2' }], ['path', { d: 'M13 11v2' }]],
  'tag': [['path', { d: 'M12.586 2.586A2 2 0 0 0 11.172 2H4a2 2 0 0 0-2 2v7.172a2 2 0 0 0 .586 1.414l8.704 8.704a2.426 2.426 0 0 0 3.42 0l6.58-6.58a2.426 2.426 0 0 0 0-3.42z' }], ['circle', { cx: '7.5', cy: '7.5', r: '.5', fill: 'currentColor' }]],
  'activity': [['path', { d: 'M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2' }]],
  'clipboard-list': [['rect', { width: '8', height: '4', x: '8', y: '2', rx: '1', ry: '1' }], ['path', { d: 'M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2' }], ['path', { d: 'M12 11h4' }], ['path', { d: 'M12 16h4' }], ['path', { d: 'M8 11h.01' }], ['path', { d: 'M8 16h.01' }]],
  'receipt-text': [['path', { d: 'M4 2v20l2-1 2 1 2-1 2 1 2-1 2 1 2-1 2 1V2l-2 1-2-1-2 1-2-1-2 1-2-1-2 1Z' }], ['path', { d: 'M14 8H8' }], ['path', { d: 'M16 12H8' }], ['path', { d: 'M13 16H8' }]],
  'undo-2': [['path', { d: 'M9 14 4 9l5-5' }], ['path', { d: 'M4 9h10.5a5.5 5.5 0 0 1 5.5 5.5a5.5 5.5 0 0 1-5.5 5.5H11' }]],
  'panel-left-close': [['rect', { width: '18', height: '18', x: '3', y: '3', rx: '2' }], ['path', { d: 'M9 3v18' }], ['path', { d: 'm16 15-3-3 3-3' }]],
  'panel-left-open': [['rect', { width: '18', height: '18', x: '3', y: '3', rx: '2' }], ['path', { d: 'M9 3v18' }], ['path', { d: 'm14 9 3 3-3 3' }]],
  'wallet': [['path', { d: 'M19 7V4a1 1 0 0 0-1-1H5a2 2 0 0 0 0 4h15a1 1 0 0 1 1 1v4h-3a2 2 0 0 0 0 4h3a1 1 0 0 0 1-1v-2a1 1 0 0 0-1-1' }], ['path', { d: 'M3 5v14a2 2 0 0 0 2 2h15a1 1 0 0 0 1-1v-4' }]],
  'messages-square': [['path', { d: 'M14 9a2 2 0 0 1-2 2H6l-4 4V4a2 2 0 0 1 2-2h8a2 2 0 0 1 2 2z' }], ['path', { d: 'M18 9h2a2 2 0 0 1 2 2v11l-4-4h-6a2 2 0 0 1-2-2v-1' }]],
  'log-out': [['path', { d: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4' }], ['polyline', { points: '16 17 21 12 16 7' }], ['line', { x1: '21', x2: '9', y1: '12', y2: '12' }]]
} satisfies Record<string, LucideIconNode>

const glyph = (name: keyof typeof nodes): LucideGlyph => ({ name, nodes: nodes[name] })

/** Icon.vue name -> Lucide glyph, in Icon.vue's order. */
export const lucideIcons = {
  play: glyph('play'),
  refresh: glyph('rotate-cw'), // the reference console's Refresh button; also spins as a loading mark
  edit: glyph('square-pen'), // pencil on a square, like pencil-square
  trash: glyph('trash-2'),
  plus: glyph('plus'),
  search: glyph('search'),
  more: glyph('ellipsis'),
  chart: glyph('chart-column'), // bar chart, same glyph as chartBar
  clock: glyph('clock'),
  link: glyph('link'),
  sync: glyph('refresh-cw'), // two-arrow loop, kept apart from refresh
  chevronDown: glyph('chevron-down'),
  chevronRight: glyph('chevron-right'),
  chevronLeft: glyph('chevron-left'),
  check: glyph('check'),
  x: glyph('x'),
  eye: glyph('eye'),
  eyeOff: glyph('eye-off'),
  cog: glyph('settings'), // the reference console's settings gear
  grid: glyph('layout-grid'), // four separate tiles, like squares-2x2
  chat: glyph('message-circle-more'),
  lightbulb: glyph('lightbulb'),
  arrowRight: glyph('arrow-right'),
  arrowLeft: glyph('arrow-left'),
  arrowUp: glyph('arrow-up'),
  arrowDown: glyph('arrow-down'),
  arrowsUpDown: glyph('arrow-up-down'),
  chevronUp: glyph('chevron-up'),
  externalLink: glyph('external-link'),
  checkCircle: glyph('circle-check'),
  xCircle: glyph('circle-x'),
  exclamationCircle: glyph('circle-alert'),
  exclamationTriangle: glyph('triangle-alert'),
  trophy: glyph('trophy'),
  infoCircle: glyph('info'),
  questionCircle: glyph('circle-help'),
  user: glyph('user'),
  userCircle: glyph('circle-user'),
  userPlus: glyph('user-plus'),
  users: glyph('users'), // Lucide has no three-person glyph
  document: glyph('file-text'),
  clipboard: glyph('clipboard'),
  copy: glyph('copy'),
  inbox: glyph('inbox'),
  download: glyph('download'),
  upload: glyph('upload'),
  filter: glyph('filter'),
  globe: glyph('globe'),
  sort: glyph('chevrons-up-down'),
  key: glyph('key-round'), // head top right like the Heroicons key; the reference API-key glyph
  lock: glyph('lock'),
  shield: glyph('shield-check'), // the Heroicons glyph carries the check too
  menu: glyph('menu'),
  calendar: glyph('calendar'),
  home: glyph('house'),
  terminal: glyph('square-terminal'), // framed prompt, like command-line
  gift: glyph('gift'),
  creditCard: glyph('credit-card'),
  mail: glyph('mail'),
  chartBar: glyph('chart-column'),
  trendingUp: glyph('trending-up'),
  database: glyph('database'),
  cube: glyph('box'), // isometric box
  bell: glyph('bell'),
  bolt: glyph('zap'),
  sparkles: glyph('sparkles'),
  cloud: glyph('cloud'),
  server: glyph('server'),
  sun: glyph('sun'),
  moon: glyph('moon'),
  book: glyph('book-open'),
  dollar: glyph('circle-dollar-sign'), // dollar in a circle, like currency-dollar
  ban: glyph('ban'),
  login: glyph('log-in'), // arrow into the door
  swap: glyph('arrow-right-left'),
  beaker: glyph('flask-conical'), // the Heroicons beaker is a conical flask
  cpu: glyph('cpu'),
  chatBubble: glyph('message-square-more'),
  calculator: glyph('calculator'),
  fire: glyph('flame'),
  badge: glyph('badge-check'),
  brain: glyph('brain'), // by name (the Heroicons glyph was a flask stand-in)
  // layout chrome (AppSidebar / AppHeader; ui-el icons/ICONS.md §5)
  dashboard: glyph('layout-dashboard'),
  camera: glyph('images'), // the batch-image and image-studio entries: pictures, by function
  folder: glyph('folder'),
  layers: glyph('layers'), // the reference's channel glyph
  coins: glyph('coins'), // the recharge / subscribe entry (upstream draws a custom coin mark)
  ticket: glyph('ticket'),
  tag: glyph('tag'),
  signal: glyph('activity'), // channel status: the reference Logs pulse line
  clipboardList: glyph('clipboard-list'),
  receipt: glyph('receipt-text'),
  undo: glyph('undo-2'), // the 推理恢复 entry: the turn-back arrow, like arrow-uturn-left
  chevronDoubleLeft: glyph('panel-left-close'), // collapse the sidebar, the reference's glyph
  chevronDoubleRight: glyph('panel-left-open'),
  banknotes: glyph('wallet'), // the header balance
  chatBubbles: glyph('messages-square'),
  logout: glyph('log-out')
}

/**
 * Icon.vue stroke widths are Heroicons weights: 1.5 by default, 2 (once 3) where a caller wants a heavier mark.
 * On Lucide glyphs they become the reference console's weights: 1.5 -> 1.8 (its regular weight), 2 -> 2 (its
 * heavier one, e.g. on chevron-right and x), linear beyond (3 -> 2.4), so emphasis is kept but compressed.
 */
export function lucideStrokeWidth(heroiconsWidth: number): number {
  return Math.round((1.8 + (Number(heroiconsWidth) - 1.5) * 0.4) * 100) / 100
}

/** The glyph's shapes; Icon.vue's <svg> carries viewBox, fill, stroke, caps and joins. */
export const LucideShapes: FunctionalComponent<{ nodes: LucideIconNode }> = (props) =>
  props.nodes.map(([shape, attrs]) => h(shape, attrs))
LucideShapes.props = ['nodes']
