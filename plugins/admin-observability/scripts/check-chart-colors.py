"""Exercise actual canvas-color reactivity without a running application."""
import argparse
import hashlib
import json
import subprocess
from pathlib import Path
from urllib.parse import urlsplit

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[3]
parser = argparse.ArgumentParser()
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
args.output.mkdir(parents=True, exist_ok=True)
bundle = args.output.resolve() / "chart-color-probe.js"
build = r'''const {createRequire}=require('node:module'),r=createRequire(process.cwd()+'/package.json');
createRequire(r.resolve('vite'))('esbuild').buildSync({stdin:{contents:"import{createApp,h}from'vue';import{usePresentationColors}from'./src/composables/usePresentationColors';const app=createApp({setup(){const colors=usePresentationColors();return()=>h('pre',{id:'colors'},JSON.stringify(colors.value))}});app.mount('#app');window.unmountProbe=()=>app.unmount();",resolveDir:process.cwd(),loader:'ts'},bundle:true,format:'iife',outfile:process.argv[1],define:{'process.env.NODE_ENV':'"production"'},alias:{vue:r.resolve('vue/dist/vue.runtime.esm-bundler.js')}});'''
built = subprocess.run(["node", "-e", build, str(bundle)], cwd=ROOT / "frontend", capture_output=True, text=True)
if built.returncode:
    raise RuntimeError(built.stderr)
html = '<!doctype html><html><head></head><body><div id="app"></div><script src="/probe.js"></script></body></html>'
theme = ROOT / "plugins/admin-observability/ui/assets/theme.css"
observed, errors, external = [], [], []
with sync_playwright() as playwright:
    browser = playwright.chromium.launch()
    try:
        page = browser.new_page()
        page.on("pageerror", lambda error: errors.append(str(error)))
        def serve(route):
            parsed = urlsplit(route.request.url)
            if parsed.hostname != "colors.test":
                external.append(route.request.url)
                route.abort()
            elif parsed.path == "/":
                route.fulfill(body=html, content_type="text/html")
            elif parsed.path == "/probe.js":
                route.fulfill(body=bundle.read_bytes(), content_type="text/javascript")
            elif parsed.path == "/theme.css":
                route.fulfill(body=theme.read_bytes(), content_type="text/css")
            else:
                route.fulfill(status=404)
        page.route("**/*", serve)
        page.goto("https://colors.test/")
        for mode, text, grid in [("light-core", "#374151", "#e5e7eb"), ("light-plugin", "#3a3b40", "#dcdcdc"), ("dark-plugin", "#dcdcdc", "#3a3b40"), ("dark-core", "#e5e7eb", "#374151")]:
            if mode == "light-plugin":
                page.evaluate("() => { const link=document.createElement('link');link.id='theme';link.rel='stylesheet';link.href='/theme.css';document.head.append(link) }")
            elif mode == "dark-plugin":
                page.evaluate("document.documentElement.classList.add('dark')")
            elif mode == "dark-core":
                page.evaluate("document.getElementById('theme').remove()")
            page.wait_for_function("expected => { const colors=JSON.parse(document.querySelector('#colors').textContent);return colors.text===expected[0]&&colors.grid===expected[1] }", arg=[text, grid])
            observed.append({"mode": mode, **json.loads(page.locator("#colors").inner_text())})
        page.evaluate("document.documentElement.style.setProperty('--theme-chart-text','url(https://invalid.test)')")
        page.wait_for_function("JSON.parse(document.querySelector('#colors').textContent).text==='#e5e7eb'")
        page.evaluate("unmountProbe();document.documentElement.classList.remove('dark')")
        assert not errors and not external, (errors, external)
    finally:
        browser.close()
report = {"offline_only": True, "cases": observed, "page_errors": errors, "external_requests": external, "source_sha256": hashlib.sha256((ROOT / "frontend/src/composables/usePresentationColors.ts").read_bytes()).hexdigest(), "theme_sha256": hashlib.sha256(theme.read_bytes()).hexdigest()}
(args.output / "chart-colors.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
print(json.dumps(report))
