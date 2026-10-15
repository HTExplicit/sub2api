Codex-only adapter of github.com/coder/websocket v1.8.14, copied from the locked Go module.

Original license: LICENSE.txt. Other providers retain the original dependency.

Local changes: opt-in client_max_window_bits offer and validation; bounded-window
compression with the already locked klauspost/compress implementation; narrow
window writers never enter the default 32 KiB writer pool. No protocol parser
or connection-lifecycle behavior is otherwise changed.
