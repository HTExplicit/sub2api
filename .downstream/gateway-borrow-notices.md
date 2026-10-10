# Gateway borrowing implementation sources

The native Cookie lease, two-shot STATE qualification and WebSocket anchor
behavior were adapted from [ranxi2001/sub2api](https://github.com/ranxi2001/sub2api),
commit `5ca3cca21eeaf4ca8a694a7f2f8f0ecd9575c549`, under LGPL-3.0. The source
files are `codex_gateway_pin.go`, `astra_routing_upstream.go`,
`astra_target_validation.go`, `openai_codex_state_probe.go`,
`openai_codex_ws_anchor.go`, `pelicanHtml.ts` and the pelican preview components.
The non-rotating source/target protocol was subsequently realigned with v2.10.3,
commit `fd1b5ee4eeb20961fbb783fa6f136a1704271e90`; retained host adaptations
are listed in [the scoped review](upstream-review-ranxi-v2.10.3.md).
The repository [LICENSE](../LICENSE) retains the LGPL-3.0 terms.

HTML fence selection also references `web/src/lib/render.ts` from
[iwyxdxl/gpttesticu](https://github.com/iwyxdxl/gpttesticu), commit
`1b3e237c15dcdf89c4aa66d3521f8d6485d6da4a`. Its MIT notice follows.

MIT License

Copyright (c) 2026 iwyxdxl

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
