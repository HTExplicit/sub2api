"""Offline browser acceptance of the independently packaged prompt audit UI."""
import argparse
import importlib.util
import json
import threading
from http.server import ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("prompt_ui_fixture", ROOT / "plugins/prompt-skills/scripts/check-ui.py")
fixture = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixture)
fixture.COMPILED = ROOT / "plugins/admin-observability/ui/compiled"
fixture.HARNESS = r'''<!doctype html><html><head><meta charset="utf-8"><style>body{margin:0}iframe{width:100%;height:960px;border:0;display:block}</style></head><body>
<iframe title="Audit plugin" sandbox="allow-scripts allow-forms" src="/ui/index.html#bridge_token=fixture"></iframe><script>
const frame=document.querySelector('iframe'),options=new URLSearchParams(location.search),context={locale:'zh',theme:options.get('theme')||'light',available:true,contribution_id:'prompt-audit',table_page_size_options:[20,50]};
let config={enabled:true,blocking_enabled:false,blocking_latest_turn_only:false,store_pass_events:false,effective_mode:'async_audit',strategy:'priority',worker_count:4,queue_capacity:100,scanners:['jailbreak'],all_groups:true,group_ids:[],endpoints:[{id:'guard-1',name:'合成审计节点',protocol:'openai_compatible',base_url:'https://fixture.invalid',model:'guard',timeout_ms:3000,input_limit:4000,enabled:true,has_token:true,token_status:'configured'}],config_version:7,updated_at:'2026-09-20T00:00:00Z',updated_by:1,change_summary:'{}'};
const runtime={process_status:'running',effective_mode:'async_audit',expected_config_version:7,active_config_version:7,worker_total:4,worker_active:1,queue_capacity:100,queue:{staging:0,queued:0,processing:0,retry:0,done:5,failed:0,active:0},processed_total:5,failed_total:0,enqueued_total:5,dropped_total:0,database_status:'ok',redis_status:'ok',endpoints:{},guard_metrics:{total:1,allowed:1,flagged:0,blocked:0,unavailable:0,invalid:0,timeouts:0,failovers:0,bulkhead_full:0,record_failed:0}};
window.calls=[];window.saved=[];
window.setPluginAvailable=value=>{context.available=value;frame.contentWindow.postMessage({source:'sub2api-plugin-host',bridge_token:'fixture',type:'extension.context.updated',context},'*')};
addEventListener('message',event=>{
 const m=event.data;if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 if(m.type==='ui.resize'){frame.style.height=Math.min(1200,Math.max(640,Number(m.height)))+'px';return;}
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',type:m.type+'.result',request_id:m.request_id,ok:true};
 if(m.type==='extension.context')reply.context=context;
 else if(m.type==='preference.read')reply.value='20';
 else if(m.type==='preference.write')reply.value=m.value;
 else if(m.type==='extension.resource'){
  window.calls.push(m.operation);
  if(m.operation==='prompt-audit.config')reply.result=config;
  else if(m.operation==='prompt-audit.runtime')reply.result=runtime;
  else if(m.operation==='prompt-audit.groups')reply.result=[];
  else if(m.operation==='prompt-audit.events')reply.result={items:[],total:0,page:1,page_size:20,pages:0};
  else if(m.operation==='prompt-audit.update'){window.saved.push(m.input.body);config={...config,...m.input.body,config_version:8,endpoints:m.input.body.endpoints.map(({token,clear_token,...rest})=>({...rest,has_token:true,token_status:'configured'}))};reply.result=config;}
  else {reply.ok=false;reply.error='Unsupported fixture operation';}
 }else return;
 event.source.postMessage(reply,'*');
});
</script></body></html>'''


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    assert (fixture.COMPILED / "index.html").is_file()
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), fixture.Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    cases = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for width in (1200, 390):
                    for theme in ("light", "dark"):
                        page = browser.new_page(viewport={"width": width, "height": 1000})
                        errors, external = [], []
                        page.on("pageerror", lambda error: errors.append(str(error)))
                        def offline(route):
                            if urlsplit(route.request.url).hostname != "127.0.0.1":
                                external.append(route.request.url)
                                route.abort()
                            else:
                                route.continue_()
                        page.route("**/*", offline)
                        page.goto(f"http://127.0.0.1:{server.server_port}/?theme={theme}")
                        frame = page.frame_locator("iframe")
                        expect(frame.locator('[data-test="tab-events"]')).to_have_attribute("aria-selected", "true")
                        assert frame.get_by_role("tab").all_text_contents() == ["事件", "配置"]
                        frame.locator('[data-test="tab-config"]').click()
                        frame.get_by_role("button", name="编辑", exact=True).click()
                        name, token = frame.get_by_label("节点名称", exact=True), frame.get_by_label("API Key", exact=True)
                        expect(token).to_have_value("")
                        name.fill("更新后的合成节点")
                        token.fill("synthetic-replacement-only")
                        page.evaluate("setPluginAvailable(false)")
                        expect(frame.locator("#app")).to_have_attribute("inert", "")
                        expect(token).to_have_value("synthetic-replacement-only")
                        page.evaluate("setPluginAvailable(true)")
                        expect(frame.locator("#app")).not_to_have_attribute("inert", "")
                        assert frame.locator("body").evaluate("node=>node.scrollWidth<=innerWidth")
                        page.screenshot(path=str(args.output / f"audit-dialog-{width}-{theme}.png"), full_page=True)
                        frame.locator('[data-test="save-endpoint"]').click()
                        frame.locator('[data-test="save-config"]').click()
                        page.wait_for_function("saved.length===1")
                        assert page.evaluate("saved[0].expected_config_version") == 7
                        assert page.evaluate("saved[0].endpoints[0].token") == "synthetic-replacement-only"
                        frame.get_by_role("button", name="编辑", exact=True).click()
                        expect(token).to_have_value("")
                        frame.get_by_role("button", name="取消", exact=True).click()
                        expect(frame.get_by_role("dialog")).not_to_be_visible()
                        assert not errors and not external, (errors, external)
                        cases.append({"width": width, "theme": theme, "input_preserved": True, "version_fence": 7, "saved_token_not_echoed": True, "page_errors": errors, "external_requests": external})
                        page.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
    evidence = {"offline_only": True, "cases": cases, "visual_inspection": "not performed; screenshots and DOM assertions recorded separately"}
    (args.output / "audit-ui-check.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(evidence, ensure_ascii=False))


if __name__ == "__main__":
    main()
