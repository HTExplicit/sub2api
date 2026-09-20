"""Offline image UI checks using native Blob transport and synthetic jobs."""
import argparse
import json
import mimetypes
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[3]
COMPILED = ROOT / 'plugins/image-tools/ui/compiled'
HARNESS = r'''<!doctype html><html><head><meta charset="utf-8"><style>
body{margin:0}iframe{width:100%;height:1100px;border:0;display:block}
</style></head><body><iframe sandbox="allow-scripts allow-forms" title="Image Studio" src="/ui/index.html#bridge_token=fixture"></iframe><script>
const frame=document.querySelector('iframe'),q=new URLSearchParams(location.search);
const ctx={actor_id:42,locale:'zh',theme:q.get('theme')||'dark',available:true,retained_controls:true,contribution_id:'image-studio'};
const histories=new Map();window.created=[];window.downloads=[];window.notifications=[];
window.ownerCount=id=>(histories.get(id)||[]).length;
window.changeContext=values=>{Object.assign(ctx,values);frame.contentWindow.postMessage({source:'sub2api-plugin-host',bridge_token:'fixture',type:'extension.context.updated',context:ctx},'*')};
const canvas=document.createElement('canvas');canvas.width=8;canvas.height=8;canvas.getContext('2d').fillRect(0,0,8,8);
const image=new Promise(resolve=>canvas.toBlob(resolve,'image/png'));
let job=null;
addEventListener('message',async event=>{
 const m=event.data;if(event.source!==frame.contentWindow||event.origin!=='null'||m?.bridge_token!=='fixture')return;
 if(m.type==='ui.resize'){frame.style.height=Math.min(1200,Math.max(600,m.height))+'px';return;}
 if(m.type==='ui.notify'){window.notifications.push(m.message);return;}
 const reply={source:'sub2api-plugin-host',bridge_token:'fixture',request_id:m.request_id,type:m.type+'.result',ok:true};
 if(m.type==='extension.context')reply.context=ctx;
 else if(m.type==='ui.confirm')reply.confirmed=true;
 else if(m.type==='ui.download'){window.downloads.push({type:m.blob.type,bytes:m.blob.size});}
 else if(m.type==='extension.resource'){
  const records=histories.get(ctx.actor_id)||[];
  switch(m.operation){
   case 'image.keys':reply.result={items:[{api_key:{id:9,name:'图像密钥',group_id:10,group:{id:10,name:'测试分组'}},capabilities:[{object:'model_capability',id:'gpt-image-2',kind:'image',input_modalities:['text'],output_modalities:['image'],endpoints:['images.generations'],client_surfaces:['image_studio'],controls:{generation:{sizes:['1024x1024'],qualities:['low'],max_output_count:4}}}]}]};break;
   case 'image.jobs':reply.result={items:[]};break;
   case 'image.create':window.created.push(Object.fromEntries(m.input.form));job={id:41,api_key_id:9,mode:'generate',model:'gpt-image-2',count:1,status:'pending',counts:{processed:0,succeeded:0,failed:0,canceled:0},created_at:'2026-09-20T00:00:00Z'};reply.result=job;break;
   case 'image.job':reply.result={job:{...job,status:'succeeded',counts:{processed:1,succeeded:1,failed:0,canceled:0}},items:[],artifacts:[{id:52,job_id:41,kind:'output',content_type:'image/png',byte_size:(await image).size,download_url:'/unused'}]};break;
   case 'image.artifact':reply.result=await image;break;
   case 'image.history.list':reply.result=records;break;
   case 'image.history.save':histories.set(ctx.actor_id,[m.input.local_data,...records.filter(r=>r.id!==m.input.local_data.id)]);reply.result=true;break;
   case 'image.history.delete':histories.set(ctx.actor_id,records.filter(r=>r.id!==m.input.local_data));reply.result=true;break;
   case 'image.history.clear':histories.set(ctx.actor_id,[]);reply.result=true;break;
   default:reply.ok=false;reply.error='Unsupported fixture operation '+m.operation;
  }
 }else return;
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
            assets = {'/ui/' + f.relative_to(COMPILED).as_posix(): f for f in COMPILED.rglob('*') if f.is_file()}
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
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    cases = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch()
            try:
                for width in (1200, 390):
                    for theme in ('light', 'dark'):
                        page = browser.new_page(viewport={'width': width, 'height': 960})
                        errors, external = [], []
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        def route_request(route):
                            if urlsplit(route.request.url).hostname != '127.0.0.1':
                                external.append(route.request.url)
                                route.abort()
                            else:
                                route.continue_()
                        page.route('**/*', route_request)
                        page.goto(f'http://127.0.0.1:{server.server_port}/?theme={theme}')
                        frame = page.frame_locator('iframe')
                        expect(frame.locator('[data-testid="api-key-select"]')).to_be_visible()
                        labels = frame.get_by_role('button').all_text_contents()
                        assert labels
                        frame.locator('[data-testid="api-key-select"]').select_option('9')
                        frame.locator('[data-testid="prompt-input"]').fill('离线灯塔图片')
                        frame.locator('[data-testid="submit-image"]').click()
                        expect(frame.locator('[data-testid="history-grid"] img')).to_be_visible()
                        page.wait_for_function('ownerCount(42)===1')
                        assert page.evaluate("created[0].api_key_id==='9' && !('api_key' in created[0])")
                        page.evaluate('changeContext({available:false})')
                        expect(frame.locator('[data-testid="submit-image"]')).to_be_disabled()
                        expect(frame.locator('[data-testid="history-grid"] img')).to_be_visible()
                        expect(frame.locator('[data-testid="clear-history"]')).to_be_enabled()
                        assert frame.locator('body').evaluate('node=>node.scrollWidth<=innerWidth')
                        page.screenshot(path=str(args.output / f'image-{width}-{theme}.png'), full_page=True)
                        page.evaluate('changeContext({actor_id:88,available:true})')
                        expect(frame.locator('[data-testid="empty-history"]')).to_be_visible()
                        assert page.evaluate('ownerCount(42)') == 1
                        assert not errors, errors
                        assert not external, external
                        cases.append({'width': width, 'theme': theme, 'blob_roundtrip': True, 'owner_isolated': True, 'page_errors': errors, 'external_requests': 0})
                        page.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
    result = {'offline_only': True, 'cases': cases}
    (args.output / 'image-ui-check.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps(result, ensure_ascii=False))


if __name__ == '__main__':
    main()
