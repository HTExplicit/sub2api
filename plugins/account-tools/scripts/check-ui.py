"""Offline checks of independent account tools, including inline frame sizing."""
import argparse
import json
import mimetypes
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
COMPILED = ROOT / "plugins/account-tools/ui/compiled"
HARNESS = r'''<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0}iframe{width:100%;height:900px;border:0;display:block}
</style></head><body><iframe sandbox="allow-scripts allow-forms" title="Account tools" src="/ui/index.html#bridge_token=fixture"></iframe><script>
const frame=document.querySelector('iframe'),q=new URLSearchParams(location.search),mode=q.get('mode');
const context={locale:'zh',theme:q.get('theme')||'dark',available:true,contribution_id:mode,layout:mode==='account-taxonomy-navigation'?'inline':'page',view_props:{
 folders:[{id:3,name:'生产账号',sort_order:0,account_count:1,created_at:'',updated_at:''}],tags:[{id:5,name:'已付费',sort_order:0,account_count:1,created_at:'',updated_at:''}],
 activeFolder:'',total:2,uncategorizedCount:1,target:{mode:'selected',accountIds:[1,2],count:2},accountIds:[1,2],accountId:1,folderId:null,tagIds:[]
}};
window.events=[];window.jobs=[];window.writes=[];window.preference='';window.notifications=[];
addEventListener('message',event=>{
 const m=event.data;if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 if(m.type==='ui.resize'){frame.style.height=Math.min(1200,Math.max(0,m.height))+'px';return;}
 if(m.type==='ui.notify'){window.notifications.push(m.message);return;}
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',request_id:m.request_id,type:m.type+'.result',ok:true};
 if(m.type==='extension.context')reply.context=context;
 else if(m.type==='preference.read')reply.value=window.preference;
 else if(m.type==='preference.write')window.preference=m.value;
 else if(m.type==='extension.event')window.events.push({name:m.name,payload:m.payload});
 else if(m.type==='extension.job.open')window.jobs.push(m.job_id);
 else if(m.type==='extension.resource'){
  const body=m.input.body||{};
  if(m.operation==='tests.models')reply.result={items:body.account_ids.map(id=>({account_id:id,name:'账号 '+id,platform:'openai',type:'oauth',is_cindy:false,models:[{id:'model-a',type:'model',display_name:'Model A',reasoning_efforts:['low','high'],default_reasoning_effort:'low'}]}))};
  else if(m.operation==='tests.submit'||m.operation==='taxonomy.bulk.update'){window.writes.push(m);reply.result={id:77,metadata:{plugin_id:1},status:'pending'};}
  else if(m.operation==='taxonomy.folders.create'){window.writes.push(m);reply.result={id:4,name:body.name,sort_order:0,account_count:0};}
  else if(m.operation==='taxonomy.account.update'){window.writes.push(m);reply.result={account_id:1};}
  else if(m.operation==='import.preview'){reply.result={create_count:1,update_count:0,reject_count:0,items:[{index:0,name:'redacted',action:'create'}]};}
  else if(m.operation==='import.submit'){window.writes.push(m);reply.result={id:77,metadata:{plugin_id:1},status:'pending'};}
  else {reply.ok=false;reply.error='Unsupported fixture operation '+m.operation;}
 } else return;
 event.source.postMessage(reply,'*');
});
</script></body></html>'''


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        target = urlsplit(self.path).path
        if target == "/":
            data, mime = HARNESS.encode(), "text/html; charset=utf-8"
        else:
            assets = {"/ui/" + file.relative_to(COMPILED).as_posix(): file for file in COMPILED.rglob("*") if file.is_file()}
            if target not in assets:
                self.send_error(404)
                return
            data, mime = assets[target].read_bytes(), mimetypes.guess_type(target)[0] or "application/octet-stream"
        self.send_response(200)
        self.send_header("Content-Type", mime)
        self.send_header("Cache-Control", "no-store")
        if target.startswith("/ui/"):
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Cross-Origin-Resource-Policy", "cross-origin")
            self.send_header("Content-Security-Policy", "default-src 'none'; script-src 'self' 'unsafe-inline'; worker-src blob:; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
        self.end_headers()
        self.wfile.write(data)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--mode")
    parser.add_argument("--theme", default="dark", choices=["dark", "light"])
    parser.add_argument("--width", type=int)
    parser.add_argument("--skip", action="append", default=[])
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    results = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                modes = ['account-taxonomy-navigation', 'account-taxonomy-manager', 'account-taxonomy-bulk', 'account-batch-test', 'account-taxonomy-edit', 'account-import']
                for width in ([args.width] if args.width else (1200, 390)):
                    for mode in modes:
                        if args.mode and args.mode != mode:
                            continue
                        if f'{mode}:{width}' in args.skip:
                            continue
                        page = browser.new_page(viewport={"width": width, "height": 920})
                        errors, external = [], []
                        workers = []
                        page.on('worker', lambda worker: workers.append(worker.url))
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        def route_request(route):
                            if urlsplit(route.request.url).hostname != '127.0.0.1':
                                external.append(route.request.url)
                                route.abort()
                            else:
                                route.continue_()
                        page.route('**/*', route_request)
                        page.goto(f'http://127.0.0.1:{server.server_port}/?mode={mode}&theme={args.theme}')
                        frame = page.frame_locator('iframe')
                        if mode == 'account-import':
                            expect(frame.locator('#account-import-job-form')).to_be_visible()
                        else:
                            expect(frame.locator('#app')).not_to_be_empty()
                        labels = frame.get_by_role('button').all_text_contents()
                        assert labels
                        if mode == 'account-taxonomy-navigation':
                            if width < 1024:
                                frame.locator('[aria-haspopup="listbox"]').click()
                                choice = frame.get_by_role('option').filter(has_text='生产账号')
                            else:
                                choice = frame.locator('[data-test="desktop-taxonomy-option"]').filter(has_text='生产账号')
                            expect(choice).to_be_visible()
                            box = choice.bounding_box()
                            assert box['y'] + box['height'] <= page.locator('iframe').bounding_box()['height'] + 2
                            choice.click()
                            page.wait_for_function("events.some(item=>item.name==='select'&&item.payload==='3')")
                        elif mode == 'account-import':
                            imported = {'type': 'sub2api-data', 'version': 2, 'exported_at': '2030-01-01T00:00:00Z', 'accounts': [{'name': 'Fixture', 'platform': 'openai', 'type': 'oauth', 'credentials': {'access_token': 'synthetic-token'}}], 'proxies': []}
                            frame.locator('input[type="file"]').set_input_files({'name': 'fixture.json', 'mimeType': 'application/json', 'buffer': json.dumps(imported).encode()})
                            expect(frame.locator('[data-test="preview-import"]')).to_be_enabled()
                            frame.locator('[data-test="preview-import"]').click()
                            expect(frame.locator('[data-test="import-preview"]')).to_be_visible()
                            frame.locator('[data-test="submit-import-job"]').click()
                            page.wait_for_function('jobs.includes(77)')
                            assert page.evaluate("writes[0].operation==='import.submit' && writes[0].input.body.data.accounts[0].credentials.access_token==='synthetic-token'")
                            assert workers, 'Import parsing must execute in the packaged worker'
                        elif mode == 'account-taxonomy-manager':
                            frame.locator('form input').fill('新分类')
                            frame.locator('form button[type="submit"]').click()
                            page.wait_for_function("events.some(item=>item.name==='changed')")
                        elif mode == 'account-taxonomy-bulk':
                            frame.locator('[data-test="bulk-taxonomy-folder-set"]').check()
                            frame.locator('[data-test="bulk-taxonomy-folder-select"]').select_option('3')
                            frame.locator('[data-test="bulk-taxonomy-submit"]').click()
                            page.wait_for_function('jobs.includes(77)')
                            assert page.evaluate('Boolean(writes[0].input.operation_key)')
                        elif mode == 'account-batch-test':
                            expect(frame.locator('[data-account-id]')).to_have_count(2)
                            frame.locator('textarea').fill('离线测试提示词')
                            frame.locator('[data-account-id="1"] select').select_option('high')
                            frame.locator('button[form="batch-test-accounts"]').click()
                            page.wait_for_function('jobs.includes(77)')
                            assert page.evaluate('writes[0].input.body.items[0].reasoning_effort') == 'high'
                            assert page.evaluate('preference') == '离线测试提示词'
                        else:
                            frame.locator('select').select_option('3')
                            frame.get_by_role('button').first.click()
                            page.wait_for_function("events.some(item=>item.name==='changed')")
                        assert frame.locator('body').evaluate('node=>node.scrollWidth<=innerWidth')
                        assert not errors, errors
                        assert not external, external
                        if mode == 'account-taxonomy-manager' and width == 1200:
                            direct = []
                            page.on('request', lambda request: direct.append(request.url) if '/blocked-native-submit' in request.url else None)
                            frame.locator('body').evaluate("node=>{const form=document.createElement('form');form.action='/blocked-native-submit';document.body.append(form);form.submit();form.remove()}")
                            expect(frame.locator('form input')).to_be_visible()
                            assert not direct, 'CSP must block native network form submission'
                        page.screenshot(path=str(args.output / f'{mode}-{width}.png'), full_page=True)
                        results.append({"mode": mode, "width": width, "theme": args.theme, "page_errors": errors, "external_requests": 0})
                        page.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
    output = {"offline_only": True, "cases": results}
    (args.output / 'account-tools-ui.json').write_text(json.dumps(output, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(output, ensure_ascii=False))


if __name__ == '__main__':
    main()
