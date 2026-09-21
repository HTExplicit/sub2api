"""Offline browser checks for the independently packaged Cindy interfaces."""
import argparse
import json
import mimetypes
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
COMPILED = ROOT / 'plugins/cindy-provider/ui/compiled'
HARNESS = r'''<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0}iframe{width:100%;height:1000px;border:0;display:block}
</style></head><body><iframe sandbox="allow-scripts allow-forms" title="Cindy" src="/ui/index.html#bridge_token=fixture"></iframe><script>
let frame=document.querySelector('iframe');const query=new URLSearchParams(location.search),mode=query.get('mode');
const context={locale:'zh',theme:query.get('theme'),available:true,contribution_id:mode,layout:mode==='cindy-group-audit'?'page':'inline',retained_controls:mode==='cindy-balance-probe',view_props:{selectedIds:[9,10],filters:{statuses:['active']},initiallyExpanded:true}};
const counts={pending:1,running:0,healthy:1,recovered:0,exhausted:0,inconclusive:0,skipped:0};
const job={id:7,status:'running',scope:{mode:'selected',account_ids:[9,10]},rate_rps:0.5,candidate_count:2,candidate_fingerprint:'fixture',request_count:1,consecutive_upstream_failures:0,created_at:'2030-01-01T00:00:00Z',updated_at:'2030-01-01T00:00:00Z',counts};
const group={group_id:7,group_name:'混合分组',status:'active',classification:'mixed',cindy_account_count:2,ordinary_account_count:1,api_key_count:1};
const audit={summary:{pure_cindy_groups:0,mixed_groups:1,no_cindy_groups:0},groups:[group]};
window.writes=[];window.events=[];window.notifications=[];window.drafts={};
window.remount=()=>{const previous=frame;frame=previous.cloneNode(false);previous.replaceWith(frame)};
window.setAvailability=value=>{context.available=value;frame.contentWindow.postMessage({source:'sub2api-plugin-host',bridge_token:'fixture',type:'extension.context.updated',context},'*')};
addEventListener('message',event=>{
 const m=event.data;if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 if(m.type==='ui.resize'){frame.style.height=Math.min(1800,Math.max(400,m.height))+'px';return;}
 if(m.type==='ui.notify'){window.notifications.push(m.message);return;}
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',request_id:m.request_id,type:m.type+'.result',ok:true};
 if(m.type==='extension.context')reply.context=context;
 else if(m.type==='preference.read')reply.value=window.drafts[m.key]??null;
 else if(m.type==='preference.write')window.drafts[m.key]=m.value;
 else if(m.type==='extension.event')window.events.push(m.name);
 else if(m.type==='extension.resource'){
  const body=m.input?.body||{},op=m.operation;
  if(op==='cindy.duplicates')reply.result=[{identity_hash:'abcdef0123456789'.repeat(4),proposed_owner_id:9,other_account_ids:[10]}];
  else if(op==='cindy.probe.list')reply.result={items:[job],total:1};
  else if(op==='cindy.probe.items')reply.result={items:[{id:1,job_id:7,account_id:9,ordinal:1,state:'healthy',request_count:1,updated_at:job.updated_at}],total:1,page:1,page_size:20};
  else if(op==='cindy.probe.get')reply.result=job;
  else if(op==='cindy.probe.preview')reply.result={scope:body.scope,candidate_count:2,marked_count:0,unmarked_count:2,candidate_fingerprint:'fixture',minimum_calls:2,maximum_calls:4,rate_rps:body.rate_rps,minimum_eta_seconds:4,maximum_eta_seconds:8};
  else if(op==='cindy.probe.create'){window.writes.push(m);reply.result=job;}
  else if(op==='cindy.probe.pause'){job.status='paused';window.writes.push(m);reply.result=job;}
  else if(op==='cindy.probe.cancel'){job.status='cancel_requested';window.writes.push(m);reply.result=job;}
  else if(op==='cindy.groups.audit')reply.result=audit;
  else if(op==='cindy.groups.keys')reply.result={items:[{id:19,name:'迁移 Key',display_key:'sk-****abcd',status:'active'}],total:1,page:1,pages:1,page_size:100};
  else if(op==='cindy.groups.preview')reply.result={source_group_id:7,source_group_name:group.group_name,source_keeps:body.source_keeps,target_name:body.target_name,target_classification:'no_cindy',member_fingerprint:'fixture-members',cindy_account_count:2,ordinary_account_count:1,accounts_to_move:1,source_api_key_count:1,api_keys_to_rebind:body.api_key_ids.length,api_keys_remaining:1-body.api_key_ids.length};
  else if(op==='cindy.groups.split'){window.writes.push(m);reply.result={source_group_id:7,target_group_id:8};}
  else {reply.ok=false;reply.error='Unknown fixture operation '+op;}
 } else return;
 event.source.postMessage(reply,'*');
});
</script></body></html>'''


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        target = urlsplit(self.path).path
        if target == '/':
            data, mime = HARNESS.encode(), 'text/html; charset=utf-8'
        else:
            assets = {'/ui/' + file.relative_to(COMPILED).as_posix(): file for file in COMPILED.rglob('*') if file.is_file()}
            if target not in assets:
                self.send_error(404)
                return
            data, mime = assets[target].read_bytes(), mimetypes.guess_type(target)[0] or 'application/octet-stream'
        self.send_response(200)
        self.send_header('Content-Type', mime)
        self.send_header('Cache-Control', 'no-store')
        if target.startswith('/ui/'):
            self.send_header('Access-Control-Allow-Origin', '*')
            self.send_header('Cross-Origin-Resource-Policy', 'cross-origin')
            self.send_header('Content-Security-Policy', "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
        self.end_headers()
        self.wfile.write(data)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--mode', choices=['cindy-balance-probe', 'cindy-duplicate-inventory', 'cindy-group-audit'])
    parser.add_argument('--paired-variants', action='store_true', help='Only desktop/light and narrow/dark for changed interaction checks')
    parser.add_argument('--width', type=int, choices=[1200, 390])
    parser.add_argument('--theme', choices=['light', 'dark'])
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    origin = f'http://127.0.0.1:{server.server_port}'
    modes = [args.mode] if args.mode else ['cindy-balance-probe', 'cindy-duplicate-inventory', 'cindy-group-audit']
    cases = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for mode in modes:
                    for width in [args.width] if args.width else [1200, 390]:
                        for theme in [args.theme] if args.theme else ['light', 'dark']:
                            if args.paired_variants and (width, theme) not in [(1200, 'light'), (390, 'dark')]:
                                continue
                            page = browser.new_page(viewport={'width': width, 'height': 1000})
                            errors, external = [], []
                            page.on('pageerror', lambda error: errors.append(str(error)))
                            def route(request):
                                if request.request.url.startswith(origin + '/'):
                                    request.continue_()
                                else:
                                    external.append(request.request.url)
                                    request.abort()
                            page.route('**/*', route)
                            page.goto(f'{origin}/?mode={mode}&theme={theme}')
                            frame = page.frame_locator('iframe')
                            if mode == 'cindy-balance-probe':
                                expect(frame.locator('[data-test="cindy-probe-panel"]')).to_be_visible()
                                frame.locator('[data-test="cindy-probe-scope-selected"]').click()
                                frame.locator('[data-test="cindy-probe-rate"]').press('End')
                                page.wait_for_function('JSON.parse(drafts["cindy-balance-probe"] || "{}").rate_rps === 1')
                                page.evaluate('remount()')
                                expect(frame.locator('[data-test="cindy-probe-rate"]')).to_have_value('1')
                                expect(frame.locator('[data-test="cindy-probe-preview-result"]')).to_have_count(0)
                                frame.locator('[data-test="cindy-probe-preview"]').click()
                                frame.locator('[data-test="cindy-probe-create"]').click()
                                page.wait_for_function('writes.length===1')
                                assert page.evaluate('JSON.stringify(writes[0].input.body.scope.account_ids)') == '[9,10]'
                                assert page.evaluate('writes[0].input.body.rate_rps') == 1
                                page.evaluate('setAvailability(false)')
                                expect(frame.locator('[data-test="cindy-probe-preview"]')).to_be_disabled()
                                expect(frame.locator('[data-test="cindy-probe-job-detail"]')).to_be_visible()
                                expect(frame.locator('[data-test="cindy-probe-pause"]')).to_be_enabled()
                                frame.locator('[data-test="cindy-probe-cancel"]').click()
                                expect(frame.locator('[role="dialog"]')).to_contain_text('#7')
                                page.screenshot(path=str(args.output / f'{mode}-cancel-{width}-{theme}.png'), full_page=True)
                                frame.get_by_role('button', name='取消任务', exact=True).click()
                                page.wait_for_function('writes.length===2')
                                assert page.evaluate('writes[1].operation') == 'cindy.probe.cancel'
                            elif mode == 'cindy-duplicate-inventory':
                                expect(frame.locator('[data-testid="cindy-duplicate-inventory"]')).to_be_visible()
                                expect(frame.locator('[data-testid="cindy-duplicate-inventory"]')).to_contain_text('9')
                                assert 'abcdef0123456789' * 4 not in frame.locator('body').inner_text()
                            else:
                                frame.locator('[data-test="cindy-group-split-7"]').click()
                                frame.locator('[data-test="cindy-group-target-name"]').fill('保留的拆分输入')
                                frame.locator('[data-test="cindy-group-api-key-19"]').check()
                                page.wait_for_function('Boolean(drafts["cindy-group-split"])')
                                page.evaluate('remount()')
                                expect(frame.locator('[data-test="cindy-group-target-name"]')).to_have_value('保留的拆分输入')
                                expect(frame.locator('[data-test="cindy-group-api-key-19"]')).to_be_checked()
                                expect(frame.locator('[data-test="cindy-group-split-preview"]')).to_have_count(0)
                                frame.locator('[data-test="cindy-group-split-preview-button"]').click()
                                expect(frame.locator('[data-test="cindy-group-split-preview"]')).to_be_visible()
                                expect(frame.locator('[data-test="cindy-group-api-keys"]')).to_contain_text('sk-****abcd')
                                page.evaluate('setAvailability(false)')
                                expect(frame.locator('[data-test="cindy-group-target-name"]')).to_have_value('保留的拆分输入')
                                expect(frame.locator('.modal-footer')).to_have_attribute('inert', '')
                                page.screenshot(path=str(args.output / f'{mode}-retained-{width}-{theme}.png'), full_page=True)
                                page.evaluate('setAvailability(true)')
                                expect(frame.locator('.modal-footer')).not_to_have_attribute('inert', '')
                                if width == 390:
                                    # The outer fixture viewport is shorter than
                                    # its auto-sized iframe. Exercise real user
                                    # scrolling, not a force/JavaScript click.
                                    page.mouse.move(4, 4)
                                    page.mouse.wheel(0, 700)
                                try:
                                    frame.locator('[data-test="cindy-group-split-submit"]').click(timeout=5000)
                                except Exception:
                                    geometry = frame.locator('[data-test="cindy-group-split-submit"]').evaluate('''button => ({
                                      viewport: { width: innerWidth, height: innerHeight },
                                      body: { width: document.body.scrollWidth, height: document.body.scrollHeight },
                                      nodes: [button, ...document.querySelectorAll('.modal-overlay,.modal-content,.modal-body,.modal-footer')].map(node => ({
                                        tag: node.tagName, class: node.className, rect: node.getBoundingClientRect().toJSON(),
                                        scrollHeight: node.scrollHeight, scrollTop: node.scrollTop, clientHeight: node.clientHeight,
                                        overflow: getComputedStyle(node).overflow, position: getComputedStyle(node).position
                                      }))
                                    })''')
                                    geometry['iframe'] = page.locator('iframe').evaluate('node => ({rect: node.getBoundingClientRect().toJSON(), style: node.style.cssText, scrollY, outerHeight: innerHeight, documentHeight: document.documentElement.scrollHeight, bodyOverflow: getComputedStyle(document.body).overflow})')
                                    (args.output / f'cindy-group-failure-{width}-{theme}.json').write_text(json.dumps(geometry, indent=2), encoding='utf-8')
                                    print(json.dumps({'failed_geometry': geometry}), flush=True)
                                    raise
                                page.wait_for_function('writes.length===1 && events.includes("split")')
                                assert page.evaluate('JSON.stringify(writes[0].input.body.api_key_ids)') == '[19]'
                            assert frame.locator('body').evaluate('node=>node.scrollWidth<=innerWidth')
                            assert not errors, errors
                            assert not external, external
                            page.screenshot(path=str(args.output / f'{mode}-{width}-{theme}.png'), full_page=True)
                            cases.append({'mode': mode, 'width': width, 'theme': theme, 'page_errors': errors, 'external_requests': 0})
                            print(json.dumps({'completed_case': cases[-1]}), flush=True)
                            page.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
    result = {'offline_only': True, 'visual_review': False, 'cases': cases}
    (args.output / ('cindy-ui-' + (args.mode or 'all') + '.json')).write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    main()
