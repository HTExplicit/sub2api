package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

var pelicanRenderSlot = make(chan struct{}, 1)

const pelicanRenderURL = "https://pelican.invalid/index.html"
const pelicanRenderCSP = "default-src 'none'; sandbox allow-scripts; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; media-src data: blob:; connect-src 'none'; frame-src 'none'; child-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"

type pelicanChromiumRenderer struct{}

// Inspect source before execution: scripts can remove their own DOM nodes.
func pelicanWorkHasScript(source string) bool {
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			return false
		}
		if kind != html.StartTagToken && kind != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		if token.Data == "script" {
			return true
		}
		for _, attr := range token.Attr {
			if strings.HasPrefix(strings.ToLower(attr.Key), "on") {
				return true
			}
		}
	}
}

func (*pelicanChromiumRenderer) Render(parent context.Context, source string) ([]PelicanImage, []byte, error) {
	select {
	case pelicanRenderSlot <- struct{}{}:
		defer func() { <-pelicanRenderSlot }()
	case <-parent.Done():
		return nil, nil, parent.Err()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	_, document := ExtractPelicanHTML(source)
	if document == "" {
		return nil, nil, fmt.Errorf("render HTML: no extractable document")
	}
	realTime := pelicanWorkHasScript(document)
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.DisableGPU, chromedp.Flag("disable-dev-shm-usage", true), chromedp.WindowSize(960, 640))
	alloc, stopAlloc := chromedp.NewExecAllocator(ctx, options...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	var served, navigationBlocked atomic.Bool
	var mainFrame atomic.Value
	// Fulfil the initial document once, in memory. Subresources and later
	// navigations can never use a network or local file request.
	chromedp.ListenTarget(browser, func(event any) {
		switch event := event.(type) {
		case *page.EventFrameRequestedNavigation:
			if served.Load() && event.FrameID == mainFrame.Load() && event.Disposition == page.ClientNavigationDispositionCurrentTab {
				navigationBlocked.Store(true)
				cancel()
			}
		case *page.EventFrameNavigated:
			if served.Load() && event.Frame.ParentID == "" && event.Frame.URL != pelicanRenderURL {
				navigationBlocked.Store(true)
				cancel()
			}
		case *fetch.EventRequestPaused:
			go func() {
				executor := cdp.WithExecutor(browser, chromedp.FromContext(browser).Target)
				if event.Request.URL == pelicanRenderURL && event.ResourceType == network.ResourceTypeDocument && served.CompareAndSwap(false, true) {
					_ = fetch.FulfillRequest(event.RequestID, 200).WithResponseHeaders([]*fetch.HeaderEntry{{Name: "Content-Type", Value: "text/html; charset=utf-8"}, {Name: "Content-Security-Policy", Value: pelicanRenderCSP}}).WithBody(base64.StdEncoding.EncodeToString([]byte(document))).Do(executor)
				} else {
					_ = fetch.FailRequest(event.RequestID, network.ErrorReasonBlockedByClient).Do(executor)
				}
			}()
		}
	})
	var cycle float64 = 3
	err := chromedp.Run(browser, fetch.Enable(), chromedp.EmulateViewport(960, 640), chromedp.ActionFunc(func(ctx context.Context) error {
		tree, err := page.GetFrameTree().Do(ctx)
		if err == nil {
			mainFrame.Store(tree.Frame.ID)
		}
		return err
	}), chromedp.Navigate(pelicanRenderURL))
	if err == nil && !realTime {
		err = chromedp.Run(browser, chromedp.Evaluate(`(() => {
 const durations=[];
 document.querySelectorAll('svg').forEach(s=>{
  s.pauseAnimations();
  s.querySelectorAll('animate,animateTransform,animateMotion').forEach(a=>{try{durations.push(a.getSimpleDuration())}catch{}});
 });
 document.getAnimations().forEach(a=>{a.pause();durations.push(Number(a.effect.getTiming().duration)/1000)});
 const usable=durations.filter(v=>Number.isFinite(v)&&v>=.5&&v<=10);
 return usable.length?Math.min(...usable):3;
})()`, &cycle))
	}
	if err != nil {
		if navigationBlocked.Load() {
			return nil, nil, fmt.Errorf("render HTML: page navigation blocked")
		}
		return nil, nil, fmt.Errorf("render HTML: %w", err)
	}
	frames := make([]PelicanImage, 0, 4)
	sheet := image.NewRGBA(image.Rect(0, 0, 1920, 1280))
	samplingStarted := time.Now()
	for i, fraction := range []float64{0, .23, .51, .79} {
		seconds := cycle * fraction
		if realTime {
			timer := time.NewTimer(time.Until(samplingStarted.Add(time.Duration(seconds * float64(time.Second)))))
			select {
			case <-ctx.Done():
				timer.Stop()
				if navigationBlocked.Load() {
					return nil, nil, fmt.Errorf("render HTML: page navigation blocked")
				}
				return nil, nil, ctx.Err()
			case <-timer.C:
			}
			seconds = time.Since(samplingStarted).Seconds()
		} else {
			err = chromedp.Run(browser, chromedp.Evaluate(fmt.Sprintf(`document.querySelectorAll('svg').forEach(s=>s.setCurrentTime(%f));document.getAnimations().forEach(a=>{a.pause();a.currentTime=%f});`, seconds, seconds*1000), nil))
			if err != nil {
				return nil, nil, err
			}
		}
		var data []byte
		if err = chromedp.Run(browser, chromedp.CaptureScreenshot(&data)); err != nil {
			if navigationBlocked.Load() {
				return nil, nil, fmt.Errorf("render HTML: page navigation blocked")
			}
			return nil, nil, err
		}
		frame, decodeErr := png.Decode(bytes.NewReader(data))
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		draw.Draw(sheet, image.Rect(i%2*960, i/2*640, (i%2+1)*960, (i/2+1)*640), frame, image.Point{}, draw.Src)
		frames = append(frames, PelicanImage{PNG: data, Time: seconds})
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, sheet); err != nil {
		return nil, nil, err
	}
	return frames, encoded.Bytes(), nil
}
