"""Offline acceptance of the compiled, sandboxed prompt management UI."""
import argparse
import json
import mimetypes
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
COMPILED = ROOT / "plugins/prompt-skills/ui/compiled"
HARNESS = r'''<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0}iframe{width:100%;height:960px;border:0;display:block}
</style></head><body><iframe title="Prompt plugin" sandbox="allow-scripts" src="/ui/index.html#bridge_token=fixture"></iframe><script>
const frame=document.querySelector('iframe'), options=new URLSearchParams(location.search);
const context={locale:'zh',theme:options.get('theme')||'light',available:true,contribution_id:'prompt-management'};
const runtime={enabled:true,expose_server_prompt:false,compact_enabled:false,template_id:2,version_id:20,template_version:1,revision:5,sha256:'a'.repeat(64),byte_length:5,degraded:false,composition_mode:'inline',bundle_id:'',bundle_manifest_sha256:'',updated_at:'2026-09-20T00:00:00Z'};
const template={id:2,slug:'fixture',name:'测试模板',description:'离线合成数据',is_seed:false,managed_source:'',created_at:runtime.updated_at,updated_at:runtime.updated_at};
let version={id:20,template_id:2,version:1,body:'初始草稿',sha256:'a'.repeat(64),byte_length:12,note:'',composition_mode:'inline',bundle_id:'',bundle_manifest_sha256:'',is_active:true,created_at:runtime.updated_at};
window.saved=[];window.calls=[];
window.setPluginAvailable=value=>{context.available=value;frame.contentWindow.postMessage({source:'sub2api-plugin-host',bridge_token:'fixture',type:'extension.context.updated',context},'*')};
addEventListener('message',event=>{
 const m=event.data;
 if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 if(m.type==='ui.resize'){frame.style.height=Math.min(1200,Math.max(640,Number(m.height)))+'px';return;}
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',type:m.type+'.result',request_id:m.request_id,ok:true};
 if(m.type==='extension.context')reply.context=context;
 else if(m.type==='extension.resource'){
  window.calls.push(m.operation);
  if(m.operation==='prompts.list')reply.result={templates:[template],runtime};
  else if(m.operation==='prompts.read')reply.result={template,versions:[version],runtime};
  else if(m.operation==='prompts.draft'){window.saved.push(m.input.body);version={...version,...m.input.body,id:21,version:2,is_active:false};reply.result=version;}
  else if(m.operation==='skills.registry')reply.result={runtime:{revision:1,degraded:false,updated_at:runtime.updated_at},versions:[],source:{upstream_source_id:'moxinggang',upstream_root:'https://example.invalid/source',public_root:'https://example.invalid/public'}};
  else {reply.ok=false;reply.error='Unsupported fixture operation: '+m.operation;}
 } else return;
 event.source.postMessage(reply,'*');
});
</script></body></html>'''


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        requested = urlsplit(self.path).path
        if requested == "/":
            content, mime = HARNESS.encode(), "text/html; charset=utf-8"
        else:
            assets = {"/ui/" + file.relative_to(COMPILED).as_posix(): file for file in COMPILED.rglob("*") if file.is_file()}
            if requested not in assets:
                self.send_error(404)
                return
            content = assets[requested].read_bytes()
            mime = mimetypes.guess_type(requested)[0] or "application/octet-stream"
        self.send_response(200)
        self.send_header("Content-Type", mime)
        self.send_header("Cache-Control", "no-store")
        if requested.startswith("/ui/"):
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cross-Origin-Resource-Policy", "cross-origin")
            self.send_header("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
        self.end_headers()
        self.wfile.write(content)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    assert (COMPILED / "index.html").is_file(), "Build the independent prompt UI first"
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    cases = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for width in (900, 390):
                    for theme in ("light", "dark"):
                        page = browser.new_page(viewport={"width": width, "height": 960})
                        errors, external = [], []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        def route_request(route):
                            if urlsplit(route.request.url).hostname != "127.0.0.1":
                                external.append(route.request.url)
                                route.abort()
                            else:
                                route.continue_()
                        page.route("**/*", route_request)
                        page.goto(f"http://127.0.0.1:{server.server_port}/?theme={theme}")
                        frame = page.frame_locator("iframe")
                        body = frame.locator('[data-test="system-prompt-body"]')
                        expect(body).to_have_value("初始草稿")
                        labels = frame.get_by_role("button").all_text_contents()
                        assert labels
                        body.fill("更新后的离线草稿")
                        frame.locator('[data-test="system-prompt-save-version"]').click()
                        page.wait_for_function("saved.length===1")
                        assert page.evaluate("saved[0].body") == "更新后的离线草稿"
                        assert page.evaluate("saved[0].expected_revision") == 5
                        body.fill("故障期间保留的输入")
                        page.evaluate("setPluginAvailable(false)")
                        expect(frame.locator("#app")).to_have_attribute("inert", "")
                        expect(body).to_have_value("故障期间保留的输入")
                        page.evaluate("setPluginAvailable(true)")
                        expect(frame.locator("#app")).not_to_have_attribute("inert", "")
                        frame.locator('[data-test="system-prompt-open-advanced"]').click()
                        expect(frame.locator('[data-test="system-prompt-advanced-drawer"]')).to_be_visible()
                        page.screenshot(path=str(args.output / f"prompt-{width}-{theme}.png"), full_page=True)
                        assert frame.locator("body").evaluate("node=>node.scrollWidth<=innerWidth"), "horizontal overflow"
                        assert not errors, errors
                        assert not external, external
                        cases.append({"width": width, "theme": theme, "saved": True, "input_preserved": True, "page_errors": errors, "external_requests": len(external)})
                        page.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
    result = {"offline_only": True, "compiled_sandbox_ui": True, "cases": cases}
    (args.output / "prompt-ui-check.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
