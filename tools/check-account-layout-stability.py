"""Offline Chromium regression for the real accounts page and two plugin frames.

Starts only this task's Vite process; all API responses and auth are synthetic.
No backend, production cookies, model calls or external requests are used.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import mimetypes
import os
import shutil
import socket
import subprocess
import sys
import time
import traceback
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlsplit

import psutil
from playwright.sync_api import expect, sync_playwright

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = "a" * 64
THEME_A, THEME_B = "b" * 64, "c" * 64
USER = {"id": 1, "username": "offline-layout", "email": "layout@example.invalid", "role": "admin", "status": "active", "balance": 0, "concurrency": 10}
FOLDERS = [{"id": n, "name": f"分类 {n:02d} · Layout group", "sort_order": n, "account_count": 8, "created_at": "2026-09-23T00:00:00Z", "updated_at": "2026-09-23T00:00:00Z"} for n in range(1, 9)]
TAGS = [{"id": n, "name": f"标签 {n} / tag {n}", "sort_order": n, "account_count": 10, "created_at": "2026-09-23T00:00:00Z", "updated_at": "2026-09-23T00:00:00Z"} for n in range(1, 7)]
GROUP = {"id": 23, "name": "Offline model group", "platform": "openai", "status": "active", "rate_multiplier": 1}
ROWS = [{"id": n, "name": f"Offline account {n:03d}", "platform": "openai", "type": "apikey", "status": "active", "schedulable": True, "concurrency": 10, "priority": 0, "rate_multiplier": 1, "credentials": {}, "extra": {}, "group_ids": [23], "groups": [GROUP], "tags": TAGS[:2], "management_folder": FOLDERS[0], "created_at": "2026-09-23T00:00:00Z", "updated_at": "2026-09-23T00:00:00Z", "notes": "Offline fixture. " * 30} for n in range(201, 301)]

PROBE = r"""(() => {
  const ids = new WeakMap(); let next = 0;
  const identify = node => { if (!node) return null; if (!ids.has(node)) ids.set(node, ++next); return ids.get(node); };
  const rect = node => { if (!node) return null; const r=node.getBoundingClientRect(); return {x:r.x,y:r.y,width:r.width,height:r.height,bottom:r.bottom}; };
  const scroll = node => node ? {top:node.scrollTop,left:node.scrollLeft,height:node.scrollHeight,client:node.clientHeight,node:identify(node)} : null;
  window.__layoutProbe = {boot:Math.random().toString(36).slice(2),context:0,resizes:[],shifts:[],fontLoads:[],headMutations:0,measure() {
    const root=document.getElementById('app'); const drawer=document.querySelector('[data-test="account-details-drawer"]');
    return {boot:this.boot,width:innerWidth,height:innerHeight,scrollY,root:rect(root),rootScroll:root?.scrollHeight,
      docWidth:document.documentElement.scrollWidth,bodyHeight:document.body?.scrollHeight,
      fontStatus:document.fonts.status,fontLoaded:document.fonts.check('14px MiSans'),
      bodyFont:getComputedStyle(document.body).fontFamily,
      links:[...document.querySelectorAll('link[data-plugin-theme]')].map(el=>({node:identify(el),href:el.getAttribute('href'),media:el.media})),
      context:this.context,headMutations:this.headMutations,
      menu:[...document.querySelectorAll('[role="listbox"], [role="menu"]')].map(rect),
      sidebar:rect(document.querySelector('aside.sidebar')),sidebarNav:scroll(document.querySelector('.sidebar-nav')),
      drawer:rect(drawer),drawerScroll:scroll(drawer?.querySelector('.overflow-y-auto')),
      table:scroll(document.querySelector('[data-test="account-list-scroll"] .table-wrapper')),
      frames:[...document.querySelectorAll('iframe')].map(el=>({src:el.getAttribute('src'),node:identify(el),...rect(el)}))};
  }};
  if (typeof FontFace !== 'undefined') {
    const load=FontFace.prototype.load;
    FontFace.prototype.load=function(...args){
      const sample={family:this.family,weight:this.weight,start:performance.now()};window.__layoutProbe.fontLoads.push(sample);
      return load.apply(this,args).then(face=>{sample.end=performance.now();return face},error=>{sample.end=performance.now();sample.error=String(error);throw error});
    };
  }
  addEventListener('message', event=>{
    const m=event.data;
    if(m?.source==='sub2api-plugin-host' && m.type==='extension.context.updated') window.__layoutProbe.context++;
    if(m?.source==='sub2api-plugin-ui' && m.type==='ui.resize') window.__layoutProbe.resizes.push({time:performance.now(),height:m.height,source:[...document.querySelectorAll('iframe')].find(el=>el.contentWindow===event.source)?.getAttribute('src')});
  });
  addEventListener('DOMContentLoaded',()=>{
    new MutationObserver(records=>window.__layoutProbe.headMutations+=records.filter(r=>[...r.addedNodes,...r.removedNodes].some(n=>n.nodeType===1 && n.matches?.('link[data-plugin-theme]'))).length).observe(document.head,{childList:true});
    try { new PerformanceObserver(list=>{for(const e of list.getEntries()) if(!e.hadRecentInput) window.__layoutProbe.shifts.push({time:e.startTime,value:e.value});}).observe({type:'layout-shift',buffered:true}); } catch {}
  });
})();"""


class Fixture:
    def __init__(self, port: int):
        self.port = port
        self.calls: list[dict] = []
        self.external: list[str] = []
        self.unhandled: list[str] = []
        self.theme = THEME_A
        self.fail_theme = False
        self.theme_failures = 0
        self.started = time.monotonic()
        manifest = json.loads((ROOT / "plugins/account-tools/manifest.source.json").read_text(encoding="utf-8"))
        wanted = {"account-taxonomy-navigation", "account-taxonomy-edit"}
        self.widgets = [dict(item, plugin_id=1, plugin_key="codexrip.account-tools", package_sha256=PACKAGE, available=True, account_scope={"version": 1, "bindings": [{"platform": "*", "account_type": "*", "rollout_percent": 100}]}) for item in manifest["contributions"] if item["id"] in wanted]

    def theme_item(self):
        return {"plugin_id": 2, "plugin_key": "codexrip.admin-observability", "package_sha256": self.theme, "id": "flat-theme", "slot": "theme", "permission": "public", "available": True, "label": {"zh": "平面主题", "en": "Flat theme"}, "stylesheet_url": f"/api/v1/settings/plugins/2/theme/{self.theme}/ui/assets/theme.css"}

    def route(self, route):
        request = route.request
        url = urlsplit(request.url)
        if url.hostname != "127.0.0.1" or url.port != self.port:
            self.external.append(request.url)
            route.abort()
            return
        path = unquote(url.path)
        headers = {"Cache-Control": "no-store", "Access-Control-Allow-Origin": "*", "Cross-Origin-Resource-Policy": "cross-origin"}
        if path == "/setup/status":
            route.fulfill(json={"needs_setup": False, "code": 0, "data": {"needs_setup": False}}, headers=headers)
            return
        if path.startswith("/api/v1/settings/plugins/2/theme/"):
            if path.endswith("theme.css") and self.fail_theme and self.theme in path:
                self.theme_failures += 1
                route.fulfill(status=503, headers=headers, content_type="text/css", body="/* offline simulated theme failure */")
                return
            relative = path.split("/ui/assets/", 1)[-1]
            target = (ROOT / "plugins/admin-observability/ui/assets" / relative).resolve()
            assert target.is_relative_to((ROOT / "plugins/admin-observability/ui/assets").resolve())
            route.fulfill(path=str(target), content_type=mimetypes.guess_type(str(target))[0] or "application/octet-stream", headers=headers)
            return
        if path.startswith("/fixture/account-tools/"):
            relative = path.removeprefix("/fixture/account-tools/")
            target = (ROOT / "plugins/account-tools/ui/compiled" / relative).resolve()
            assert target.is_relative_to((ROOT / "plugins/account-tools/ui/compiled").resolve())
            headers["Content-Security-Policy"] = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'none'; worker-src blob:; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"
            route.fulfill(path=str(target), content_type=mimetypes.guess_type(str(target))[0] or "application/octet-stream", headers=headers)
            return
        if not path.startswith("/api/v1/"):
            route.continue_()
            return
        path = path.removeprefix("/api/v1")
        self.calls.append({"path": path, "method": request.method, "at": round(time.monotonic() - self.started, 3)})
        query = parse_qs(url.query)
        if path == "/auth/me":
            data = USER
        elif path == "/admin/plugins/contributions":
            data = self.widgets + [self.theme_item()]
        elif path == "/settings/plugins":
            data = [self.theme_item()]
        elif path == "/admin/plugins/1/ui-session":
            contribution = (request.post_data_json or {}).get("contribution_id")
            assert contribution in {item["id"] for item in self.widgets}, contribution
            token = "synthetic-" + contribution
            data = {"plugin_key": "codexrip.account-tools", "package_sha256": PACKAGE, "permission": "admin", "url": f"/fixture/account-tools/index.html?contribution={contribution}#bridge_token={token}", "bridge_token": token, "ui_bridge_version": 1, "expires_at": "2099-01-01T00:00:00Z", "request_binding": "synthetic-binding"}
        elif path == "/admin/accounts":
            ids = set(query.get("account_ids", [""])[0].split(","))
            rows = [row for row in ROWS if str(row["id"]) in ids] if ids != {""} else ROWS
            data = {"items": rows, "total": len(rows), "pages": 1, "page": 1, "page_size": 100}
        elif path == "/admin/accounts/facets":
            data = {"total": len(ROWS), "uncategorized_count": 12, "platforms": [], "types": [], "statuses": [], "plans": [], "proxies": [], "tags": TAGS, "folders": FOLDERS}
        elif path == "/admin/accounts/folders":
            data = FOLDERS
        elif path == "/admin/accounts/tags":
            data = TAGS
        elif path == "/admin/groups/all":
            data = [GROUP]
        elif path.startswith("/admin/accounts/") and path.rsplit("/", 1)[-1].isdigit():
            data = next(row for row in ROWS if str(row["id"]) == path.rsplit("/", 1)[-1])
        elif path == "/admin/accounts/today-stats/batch":
            data = {"stats": {}}
        elif path == "/admin/accounts/usage/batch":
            data = {"items": [], "usage": {}, "results": {}}
        elif path.endswith("/all"):
            data = []
        elif path.endswith("/announcements") or path == "/subscriptions/active":
            data = []
        elif "jobs" in path or path == "/keys":
            data = {"items": [], "total": 0, "page": 1, "pages": 0}
        elif path == "/settings/public" or path == "/admin/settings":
            data = {"site_name": "Offline layout check", "registration_enabled": False, "custom_menu_items": [], "run_mode": "standard"}
        elif path.endswith("/models/context-capacities"):
            data = {"rows": [], "editable": True}
        elif path.endswith("/models"):
            data = []
        elif path.endswith("/api-key-visibility"):
            data = {"enabled": False, "configured": False}
        elif path in {"/admin/compliance", "/admin/payment/config", "/admin/settings/web-search-emulation", "/admin/system/check-updates"}:
            data = {"enabled": False, "update_available": False}
        elif "upstream-billing" in path or "scheduler" in path or path.endswith("/version") or path.endswith("/usage"):
            data = {}
        else:
            self.unhandled.append(path)
            data = {}
        route.fulfill(json={"code": 0, "message": "", "data": data}, headers=headers)


def own_process_cleanup(process):
    if process is None:
        return []
    try:
        parent = psutil.Process(process.pid)
    except psutil.NoSuchProcess:
        return []
    owned = parent.children(recursive=True) + [parent]
    for child in reversed(owned):
        try:
            child.terminate()
        except psutil.NoSuchProcess:
            pass
    _, alive = psutil.wait_procs(owned, timeout=4)
    for child in alive:
        try:
            child.kill()
        except psutil.NoSuchProcess:
            pass
    return [child.pid for child in owned]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--port", type=int, default=4827)
    parser.add_argument("--skip-registry", action="store_true", help="Reuse registry-refresh-passed.json when only fixing a later failed phase")
    parser.add_argument("--resume-responsive", action="store_true", help="Reuse theme-menu-passed.json and run only the remaining breakpoint return")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    sys.stdout.reconfigure(encoding="utf-8")
    with socket.socket() as check:
        check.bind(("127.0.0.1", args.port))
    fixture = Fixture(args.port)
    result = {"offline_only": True, "backend_started": False, "model_requests": 0, "passed": False, "scenarios": [], "samples": [], "frame_events": [], "page_errors": [], "console_errors": []}
    process = None
    log = (args.output / "vite.log").open("w", encoding="utf-8")
    try:
        environment = dict(os.environ, VITE_DEV_PROXY_TARGET="http://127.0.0.1:9", VITE_DEV_PORT=str(args.port))
        process = subprocess.Popen([shutil.which("node") or "node", str(ROOT / "frontend/node_modules/vite/bin/vite.js"), "--host", "127.0.0.1", "--port", str(args.port), "--strictPort"], cwd=ROOT / "frontend", env=environment, stdout=log, stderr=subprocess.STDOUT, creationflags=subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0)
        result["vite_pid"] = process.pid
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            if process.poll() is not None:
                raise RuntimeError("Vite exited; inspect vite.log")
            try:
                with socket.create_connection(("127.0.0.1", args.port), timeout=0.3):
                    break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("Vite startup timeout")
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            try:
                # A fresh context has no service worker registrations; all HTTP
                # is intercepted. Playwright's SW-block init hook itself throws
                # in opaque sandbox frames, so use a registration-free context.
                context = browser.new_context(viewport={"width": 1440, "height": 1000})
                context.route("**/*", fixture.route)
                def route_websocket(route):
                    address = urlsplit(route.url)
                    if address.hostname == "127.0.0.1" and address.port == args.port:
                        route.connect_to_server()
                    else:
                        fixture.external.append(route.url)
                        route.close()
                context.route_web_socket("**/*", route_websocket)
                context.add_init_script(PROBE)
                context.add_init_script("if(window===top){localStorage.setItem('auth_token','synthetic-layout-token');localStorage.setItem('auth_user',JSON.stringify("+json.dumps(USER)+"));localStorage.setItem('sub2api_locale','zh');localStorage.setItem('account-auto-refresh',JSON.stringify({enabled:true,interval_seconds:15}));window.__APP_CONFIG__={site_name:'Offline layout check',registration_enabled:false,custom_menu_items:[]};}")
                page = context.new_page()
                page.on("framedetached", lambda frame: result["frame_events"].append({"event": "detached", "url": frame.url, "at": round(time.monotonic()-fixture.started, 3)}))
                page.on("pageerror", lambda error: result["page_errors"].append(str(error)))
                page.on("console", lambda message: result["console_errors"].append(message.text) if message.type == "error" else None)
                page.goto(f"http://127.0.0.1:{args.port}/admin/accounts?group_id=23&page=1&page_size=100", wait_until="networkidle", timeout=45000)
                if page.locator(".driver-popover").count():
                    page.keyboard.press("Escape")
                nav_locator = page.locator('iframe[src*="contribution=account-taxonomy-navigation"]')
                expect(nav_locator).to_be_visible(timeout=35000)
                nav = nav_locator.element_handle().content_frame()
                expect(nav.locator("#app")).not_to_be_empty(timeout=15000)
                table = page.locator('[data-test="account-list-scroll"] .table-wrapper')
                expect(table).to_be_visible()
                table.evaluate("el=>el.scrollTop=el.scrollHeight*.45")
                page.wait_for_timeout(200)
                visible = [row for row in page.locator("tbody tr").all() if row.bounding_box() and table.bounding_box()["y"]+40 < row.bounding_box()["y"] < table.bounding_box()["y"]+table.bounding_box()["height"]-80]
                assert visible, "No account row inside table viewport"
                visible[0].locator("td").nth(1).click()
                drawer = page.locator('[data-test="account-details-drawer"]')
                expect(drawer).to_be_visible()
                edit_locator = drawer.locator('iframe[src*="contribution=account-taxonomy-edit"]')
                expect(edit_locator).to_be_visible(timeout=15000)
                edit = edit_locator.element_handle().content_frame()
                expect(edit.locator("select")).to_be_visible(timeout=15000)
                opened_account = drawer.locator("h2").inner_text()
                edit.locator("select").select_option("2")
                expect(edit.get_by_role("button").first).to_be_enabled()
                drawer.locator(".overflow-y-auto").first.evaluate("el=>el.scrollTop=60")
                page.locator(".sidebar-nav").evaluate("el=>el.scrollTop=120")
                for frame in [page.main_frame, nav, edit]:
                    frame.evaluate("async()=>{await document.fonts.ready}")

                def snapshot(label):
                    sample = {"label": label, "at": round(time.monotonic()-fixture.started, 3), "host": page.evaluate("()=>window.__layoutProbe.measure()"), "navigation": nav.evaluate("()=>window.__layoutProbe.measure()"), "editor": edit.evaluate("()=>window.__layoutProbe.measure()")}
                    result["samples"].append(sample)
                    return sample

                def settle(label, milliseconds=900):
                    samples = []
                    for _ in range(max(4, milliseconds//150)):
                        page.wait_for_timeout(150)
                        samples.append(snapshot(label))
                    for area in ["navigation", "editor"]:
                        tail = [sample[area]["height"] for sample in samples[-4:]]
                        assert max(tail)-min(tail) <= 1, (label, area, "unstable height", tail)
                        latest = samples[-1][area]
                        assert latest["rootScroll"] <= latest["height"]+2, (label, area, "content clipped", latest)
                        assert latest["docWidth"] <= latest["width"]+1, (label, area, "horizontal overflow", latest)
                    return samples[-1]

                baseline = settle("baseline")
                page.screenshot(path=str(args.output / "baseline.png"))
                print("Offline layout: baseline ready", flush=True)
                base_calls = len([call for call in fixture.calls if call["path"] == "/admin/plugins/contributions"])
                until = time.monotonic()+(0 if args.skip_registry else 32)
                while time.monotonic() < until:
                    page.wait_for_timeout(250)
                    snapshot("registry-15s-refresh")
                refreshed = settle("after-two-refreshes")
                refresh_calls = len([call for call in fixture.calls if call["path"] == "/admin/plugins/contributions"])-base_calls
                if args.skip_registry:
                    evidence_path = args.output / "registry-refresh-passed.json"
                    prior = json.loads(evidence_path.read_text(encoding="utf-8"))
                    assert any(item.get("name") == "two-normal-registry-refreshes" and item.get("passed") for item in prior["scenarios"])
                    result["reused_registry_evidence"] = str(evidence_path)
                else:
                    assert refresh_calls >= 2, ("normal 15s registry timers did not fire twice", refresh_calls)
                for area in ["navigation", "editor"]:
                    assert refreshed[area]["boot"] == baseline[area]["boot"], (area, "iframe reloaded")
                    assert refreshed[area]["links"] == baseline[area]["links"], (area, "same theme replaced")
                    assert refreshed[area]["height"] == baseline[area]["height"], (area, "height drift")
                    if not args.skip_registry:
                        assert refreshed[area]["context"] >= baseline[area]["context"]+2, (area, "context not refreshed")
                for name in ["table", "drawerScroll", "sidebarNav"]:
                    assert refreshed["host"][name]["node"] == baseline["host"][name]["node"]
                    assert abs(refreshed["host"][name]["top"]-baseline["host"][name]["top"]) <= 1, (name, "scroll moved")
                assert refreshed["host"]["sidebar"]["height"] == baseline["host"]["sidebar"]["height"]
                assert refreshed["host"]["sidebarNav"]["height"] == baseline["host"]["sidebarNav"]["height"]
                assert refreshed["host"]["links"] == baseline["host"]["links"]
                assert edit.locator("select").input_value() == "2", "draft reset by context refresh"
                if not args.skip_registry:
                    result["scenarios"].append({"name": "two-normal-registry-refreshes", "passed": True, "refresh_calls": refresh_calls, "seconds": 32})

                if not args.resume_responsive:
                    fixture.theme, fixture.fail_theme = THEME_B, True
                    page.evaluate("()=>window.dispatchEvent(new Event('sub2api:plugins-changed'))")
                    failed_theme = settle("theme-network-failure", 2200)
                    assert fixture.theme_failures >= 1
                    for area in ["host", "navigation", "editor"]:
                        assert failed_theme[area]["links"] == refreshed[area]["links"], (area, "last-good theme removed")
                    fixture.fail_theme = False
                    page.evaluate("()=>window.dispatchEvent(new Event('sub2api:plugins-changed'))")
                    for frame in [page.main_frame, nav, edit]:
                        frame.wait_for_function("digest=>[...document.querySelectorAll('link[data-plugin-theme]')].some(link=>link.href.includes(digest)&&link.media==='')", arg=THEME_B, timeout=15000)
                    restored = settle("theme-network-restored", 2200)
                    for area in ["host", "navigation", "editor"]:
                        assert len(restored[area]["links"]) == 1 and THEME_B in restored[area]["links"][0]["href"], (area, "theme not replaced after recovery")
                        assert restored[area]["fontStatus"] == "loaded" and restored[area]["fontLoaded"]
                    for name in ["table", "drawerScroll", "sidebarNav"]:
                        assert abs(restored["host"][name]["top"]-baseline["host"][name]["top"]) <= 1, (name, "theme update moved scroll")
                    result["scenarios"].append({"name": "theme-failure-and-recovery", "passed": True, "simulated_failures": fixture.theme_failures})
                    page.screenshot(path=str(args.output / "theme-restored.png"))

                else:
                    evidence_path = args.output / "theme-menu-passed.json"
                    prior = json.loads(evidence_path.read_text(encoding="utf-8"))
                    for scenario in ["theme-failure-and-recovery", "menu-natural-expansion-and-shrink"]:
                        assert any(item.get("name") == scenario and item.get("passed") for item in prior["scenarios"])
                    result["reused_theme_menu_evidence"] = str(evidence_path)

                page.set_viewport_size({"width": 1000, "height": 1000})
                narrow = settle("below-1024", 1400)
                if not args.resume_responsive:
                    # Close only the existing drawer to interact with its underlying menu;
                    # reopen it after checking the menu, retaining the same account fixture.
                    drawer.get_by_role("button", name="关闭", exact=True).click()
                    expect(drawer).not_to_be_visible()
                    trigger = nav.locator('[aria-haspopup="listbox"]')
                    expect(trigger).to_be_visible()
                    trigger.click()
                    expect(nav.get_by_role("listbox")).to_be_visible()
                    menu_samples = []
                    for _ in range(8):
                        page.wait_for_timeout(150)
                        menu_samples.append(nav.evaluate("()=>window.__layoutProbe.measure()"))
                    opened = menu_samples[-1]
                    assert opened["height"] > narrow["navigation"]["height"]
                    assert opened["menu"][0]["bottom"]+opened["scrollY"] <= opened["height"]+2, "menu clipped by iframe"
                    page.screenshot(path=str(args.output / "mobile-menu.png"))
                    trigger.click()
                    expect(nav.get_by_role("listbox")).not_to_be_visible()
                    page.wait_for_timeout(500)
                    closed = nav.evaluate("()=>window.__layoutProbe.measure()")
                    assert abs(closed["height"]-narrow["navigation"]["height"]) <= 1, ("menu did not shrink", closed["height"], narrow["navigation"]["height"])
                    result["scenarios"].append({"name": "menu-natural-expansion-and-shrink", "passed": True, "closed_height": closed["height"], "open_height": opened["height"]})
                page.set_viewport_size({"width": 1440, "height": 1000})
                page.wait_for_timeout(900)
                final_nav = nav.evaluate("()=>window.__layoutProbe.measure()")
                assert abs(final_nav["height"]-baseline["navigation"]["height"]) <= 1
                expect(page.locator("aside.sidebar")).to_be_visible()
                if not drawer.is_visible():
                    # The responsive table leaves its fixed scrolling viewport;
                    # its old off-screen row need not exist in the virtual DOM.
                    account_row = page.locator("tbody tr").filter(has_text="Offline account").first
                    account_row.locator("td").nth(1).click()
                    expect(drawer).to_be_visible()
                    edit_locator = drawer.locator('iframe[src*="contribution=account-taxonomy-edit"]')
                    expect(edit_locator).to_be_visible()
                    edit = edit_locator.element_handle().content_frame()
                    expect(edit.locator("select")).to_be_visible()
                final = settle("desktop-return", 1200)
                assert final["editor"]["height"] == baseline["editor"]["height"]
                assert final["host"]["sidebar"]["height"] == baseline["host"]["sidebar"]["height"]
                result["scenarios"].append({"name": "cross-lg-breakpoint-and-return", "passed": True, "widths": [1440,1000,1440]})
                result["responsive_table_scroll"] = {"before": baseline["host"]["table"], "narrow": narrow["host"]["table"], "returned": final["host"]["table"], "note": "Below lg the table uses document flow instead of a fixed scrolling viewport; refresh/theme scroll retention is verified separately."}
                page.screenshot(path=str(args.output / "final.png"))
                assert not result["page_errors"], result["page_errors"]
                assert not fixture.external, fixture.external
                assert not context.service_workers, "fixture must not install a service worker"
                result["passed"] = True
                result["browser_version"] = browser.version
                result["resize_messages"] = page.evaluate("()=>window.__layoutProbe.resizes")
                result["layout_shifts"] = page.evaluate("()=>window.__layoutProbe.shifts")
            except Exception:
                if "page" in locals():
                    page.screenshot(path=str(args.output / "failure.png"), full_page=True)
                    (args.output / "failure-dom.txt").write_text(page.locator("body").inner_text(), encoding="utf-8")
                raise
            finally:
                if "page" in locals():
                    result["font_loads"] = []
                    for frame in page.frames:
                        try:
                            result["font_loads"].append({"url": frame.url, "loads": frame.evaluate("()=>window.__layoutProbe?.fontLoads || []")})
                        except Exception:
                            pass
                browser.close()
    except Exception as error:
        result["failure"] = str(error)
        result["traceback"] = traceback.format_exc()
    finally:
        result["closed_owned_pids"] = own_process_cleanup(process)
        log.close()
        result["api_calls"], result["external_requests"], result["unhandled_api_paths"] = fixture.calls, fixture.external, sorted(set(fixture.unhandled))
        result["source_sha256"] = {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in [ROOT / "backend/pkg/extensionapi/ui/stylesheets.ts", ROOT / "backend/pkg/extensionapi/ui/presentation.ts", ROOT / "backend/pkg/extensionapi/ui/sizing.ts", ROOT / "backend/pkg/extensionapi/ui/client.ts", ROOT / "frontend/src/components/plugins/theme.ts", ROOT / "frontend/src/components/plugins/PluginFrame.vue"]}
        (args.output / "result.json").write_text(json.dumps(result, ensure_ascii=False, indent=2)+"\n", encoding="utf-8")
    print(json.dumps({"passed": result["passed"], "failure": (result.get("failure") or "")[:350], "scenarios": result["scenarios"], "page_errors": result["page_errors"], "external_requests": len(fixture.external), "unhandled_api_paths": result["unhandled_api_paths"], "output": str(args.output)}, ensure_ascii=False))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
