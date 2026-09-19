"""Offline browser acceptance for the actual sandboxed plugin assets."""

import argparse
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright


ROOT = Path(__file__).resolve().parents[3]
UI = ROOT / "plugins/codex-runtime/ui"
SDK = ROOT / "backend/pkg/extensionapi/ui"
HARNESS = """<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0}iframe{width:100%;height:900px;border:0;display:block}
</style></head><body><iframe title="Plugin" sandbox="allow-scripts" src="/ui/index.html#bridge_token=fixture"></iframe><script>
const options=new URLSearchParams(location.search), frame=document.querySelector('iframe');
let config={enabled:true,fail_closed:true,proxy_url:'socks5h://fixture:synthetic@proxy.example.test:1080',models:['gpt-6-astra','gpt-5.6-sol']};
window.saved=[];window.submitted=[];
addEventListener('message',event=>{
 const m=event.data;
 if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',type:m.type+'.result',request_id:m.request_id,ok:true};
 if(m.type==='extension.context')reply.context={locale:'zh',theme:options.get('theme')||'light',mode:options.get('mode')||'admin.settings',account_ids:[41,42],operation:'harvest'};
 else if(m.type==='config.load')reply.config=config;
 else if(m.type==='config.save'){config=m.config;window.saved.push(config);reply.config=config;}
 else if(m.type==='plugin.status')reply.result={status_json:JSON.stringify(config)};
 else if(m.type==='extension.invoke')reply.result={network_reachable:true,http_status:405,code:'target_http_status',certificate_fingerprint:'abcdef0123456789'.repeat(4),stages:[{name:'tcp',success:true},{name:'tls',success:true},{name:'http',success:false,message:'HTTP 405'}]};
 else if(m.type==='extension.job.submit'){window.submitted.push(m);reply.job={id:99,status:'queued'};}
 else return;
 event.source.postMessage(reply,'*');
});
</script></body></html>"""


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        path = urlsplit(self.path).path
        assets = {
            "/ui/index.html": (UI / "index.html", "text/html; charset=utf-8"),
            "/ui/assets/app.js": (UI / "assets/app.js", "text/javascript; charset=utf-8"),
            "/ui/assets/styles.css": (UI / "assets/styles.css", "text/css; charset=utf-8"),
            "/ui/assets/bridge.js": (SDK / "bridge.js", "text/javascript; charset=utf-8"),
        }
        if path == "/":
            content, kind = HARNESS.encode(), "text/html; charset=utf-8"
        elif path in assets:
            source, kind = assets[path]
            content = source.read_bytes()
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", kind)
        self.send_header("Cache-Control", "no-store")
        if path.startswith("/ui/"):
            self.send_header("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
            self.send_header("Cross-Origin-Resource-Policy", "cross-origin")
        self.end_headers()
        self.wfile.write(content)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    cases = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for width in (900, 390):
                    for theme in ("light", "dark"):
                        page = browser.new_page(viewport={"width": width, "height": 920})
                        errors = []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        page.goto(f"http://127.0.0.1:{server.server_port}/?theme={theme}")
                        frame = page.frame_locator("iframe")
                        frame.get_by_role("button", name="测试连接", exact=True).wait_for()
                        # Inspect the live accessibility names before interaction.
                        labels = frame.get_by_role("button").all_text_contents()
                        assert labels == ["测试连接", "复制", "清除代理并关闭开关", "保存设置"], labels
                        dropdown = frame.get_by_label("协议").bounding_box()
                        test_button = frame.get_by_role("button", name="测试连接", exact=True).bounding_box()
                        assert abs(dropdown["y"] + dropdown["height"] - test_button["y"] - test_button["height"]) <= 1
                        await_proxy = frame.get_by_role("button", name="测试连接", exact=True)
                        await_proxy.click()
                        frame.get_by_text("代理链路已连接，目标返回 HTTP 405；本次未发送账号凭据。", exact=True).wait_for()
                        assert frame.locator("body").evaluate("node=>node.scrollWidth<=innerWidth")
                        page.screenshot(path=str(args.output / f"proxy-{width}-{theme}.png"), full_page=True)
                        frame.get_by_role("button", name="清除代理并关闭开关", exact=True).click()
                        frame.get_by_text("已清除并关闭", exact=True).wait_for()
                        assert page.evaluate("saved.at(-1).enabled===false && saved.at(-1).proxy_url===''")
                        assert not errors, errors
                        cases.append({"width": width, "theme": theme, "aligned": True, "overflow": False, "clear_disables": True, "page_errors": errors})
                        page.close()
                page = browser.new_page()
                page.goto(f"http://127.0.0.1:{server.server_port}/?mode=account.actions")
                frame = page.frame_locator("iframe")
                frame.get_by_role("button", name="开始采集", exact=True).wait_for()
                assert frame.get_by_role("checkbox").count() == 3
                frame.get_by_role("button", name="开始采集", exact=True).click()
                page.wait_for_function("submitted.length===1")
                submitted = page.evaluate("submitted[0]")
                assert len(submitted["items"]) == 4
                assert {item["account_id"] for item in submitted["items"]} == {41, 42}
                assert all(item["payload"]["force"] is False for item in submitted["items"])
                page.close()
                print(json.dumps({"offline_only": True, "layout": cases, "batch_targets": len(submitted["items"])}, ensure_ascii=False))
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


if __name__ == "__main__":
    main()
