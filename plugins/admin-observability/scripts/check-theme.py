"""Offline browser acceptance of compiled host CSS and the optional theme."""
import argparse
import json
from pathlib import Path
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

parser = argparse.ArgumentParser()
parser.add_argument("--core-css", type=Path, required=True)
parser.add_argument("--evidence-dir", type=Path, required=True)
args = parser.parse_args()
module = Path(__file__).resolve().parents[1]
args.evidence_dir.mkdir(parents=True, exist_ok=True)
html = """<!doctype html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/core.css"></head>
<body><main class="p-5 space-y-4"><h1 class="text-xl font-semibold">插件主题 · Theme</h1>
<article class="card p-5"><p class="text-muted">账号与用量 · Account and usage</p><input class="input mt-3" value="saved input"><button class="btn btn-primary mt-3">保存设置 Save settings</button></article>
<div id="gradient" class="bg-gradient-to-r from-primary-500 to-primary-600 p-4 text-white rounded-xl shadow-lg">状态与进度 · Status and progress</div>
</main></body></html>"""
rows = []
page_errors = []
external_requests = []

with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    try:
        for width in [900, 390]:
            for dark in [False, True]:
                page = browser.new_page(viewport={"width": width, "height": 650})
                page.on("pageerror", lambda error: page_errors.append(str(error)))

                def serve(route):
                    parsed = urlparse(route.request.url)
                    if parsed.hostname != "theme.test":
                        external_requests.append(route.request.url)
                        route.abort()
                        return
                    if parsed.path == "/":
                        route.fulfill(body=html, content_type="text/html")
                    elif parsed.path == "/core.css":
                        route.fulfill(body=args.core_css.read_bytes(), content_type="text/css")
                    elif parsed.path == "/theme.css":
                        route.fulfill(body=(module / "ui/assets/theme.css").read_bytes(), content_type="text/css")
                    elif parsed.path.startswith("/fonts/") and parsed.path.split("/")[-1] in ["MiSans-Regular.woff2", "MiSans-Medium.woff2", "MiSans-Semibold.woff2", "MiSans-Bold.woff2"]:
                        route.fulfill(body=(module / "ui/assets/fonts" / parsed.path.split("/")[-1]).read_bytes(), content_type="font/woff2")
                    else:
                        route.fulfill(status=404)

                page.route("**/*", serve)
                page.goto("https://theme.test/", wait_until="networkidle")
                page.evaluate("dark => document.documentElement.classList.toggle('dark', dark)", dark)
                for enabled in [False, True, False]:
                    if enabled:
                        page.evaluate("""() => new Promise((resolve, reject) => { const link=document.createElement('link');link.id='theme';link.rel='stylesheet';link.href='/theme.css';link.onload=resolve;link.onerror=reject;document.head.append(link) })""")
                        page.evaluate("() => document.fonts.ready")
                    else:
                        page.evaluate("() => document.getElementById('theme')?.remove()")
                    # Buttons and cards transition their colors/radii. Wait for
                    # the actual cascade to settle after adding/removing CSS.
                    page.wait_for_function("""enabled => {
                      const button=getComputedStyle(document.querySelector('.btn-primary'));
                      const card=getComputedStyle(document.querySelector('.card'));
                      return button.backgroundColor === (enabled ? 'rgb(22, 138, 73)' : 'rgb(20, 184, 166)') && card.borderRadius === (enabled ? '0px' : '16px');
                    }""", arg=enabled, timeout=2000)
                    observed = page.evaluate("""() => ({canvas:getComputedStyle(document.body).backgroundColor,font:getComputedStyle(document.body).fontFamily,cardRadius:getComputedStyle(document.querySelector('.card')).borderRadius,inputRadius:getComputedStyle(document.querySelector('.input')).borderRadius,buttonRadius:getComputedStyle(document.querySelector('.btn')).borderRadius,accent:getComputedStyle(document.querySelector('.btn-primary')).backgroundColor,gradient:getComputedStyle(document.getElementById('gradient')).backgroundImage,overflow:document.documentElement.scrollWidth>innerWidth,input:document.querySelector('input').value})""")
                    assert observed["accent"] == ("rgb(22, 138, 73)" if enabled else "rgb(20, 184, 166)"), observed
                    assert observed["canvas"] == ({(False, False): "rgb(249, 250, 251)", (False, True): "rgb(2, 6, 23)", (True, False): "rgb(255, 255, 255)", (True, True): "rgb(17, 17, 17)"}[(enabled, dark)]), observed
                    assert observed["cardRadius"] == ("0px" if enabled else "16px"), observed
                    assert observed["buttonRadius"] == ("0px" if enabled else "12px"), observed
                    assert ("MiSans" in observed["font"]) == enabled, observed
                    assert not observed["overflow"] and observed["input"] == "saved input", observed
                    if enabled:
                        assert observed["gradient"].count("rgb(22, 138, 73)") == 2, observed
                    rows.append({"width": width, "dark": dark, "enabled": enabled, **observed})
                    if enabled:
                        page.screenshot(path=str(args.evidence_dir / f"theme-{width}-{'dark' if dark else 'light'}.png"))
                page.close()
    finally:
        browser.close()

assert not page_errors and not external_requests, (page_errors, external_requests)
report = {"cases": rows, "page_errors": page_errors, "external_requests": external_requests, "visual_review": "computed styles and geometry; screenshots captured without pixel review"}
(args.evidence_dir / "theme-check.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"passed_cases": len(rows), "page_errors": len(page_errors), "external_requests": len(external_requests)}))
