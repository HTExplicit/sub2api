package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

var pelicanRenderSlot = make(chan struct{}, 1)

type pelicanChromiumRenderer struct{}

func (*pelicanChromiumRenderer) Render(parent context.Context, svg string) ([]PelicanImage, []byte, error) {
	select {
	case pelicanRenderSlot <- struct{}{}:
		defer func() { <-pelicanRenderSlot }()
	case <-parent.Done():
		return nil, nil, parent.Err()
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.NoSandbox, chromedp.DisableGPU, chromedp.Flag("disable-dev-shm-usage", true), chromedp.WindowSize(960, 640))
	alloc, stopAlloc := chromedp.NewExecAllocator(ctx, options...)
	defer stopAlloc()
	browser, stopBrowser := chromedp.NewContext(alloc)
	defer stopBrowser()
	// Only this in-memory SVG is served; all other requests are rejected.
	chromedp.ListenTarget(browser, func(event any) {
		if request, ok := event.(*fetch.EventRequestPaused); ok {
			go func() {
				var err error
				if request.Request.URL == "https://pelican.invalid/scene.svg" && request.ResourceType == network.ResourceTypeDocument {
					err = fetch.FulfillRequest(request.RequestID, 200).WithResponseHeaders([]*fetch.HeaderEntry{{Name: "Content-Type", Value: "image/svg+xml"}, {Name: "Content-Security-Policy", Value: "default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src data:; base-uri 'none'"}}).WithBody(base64.StdEncoding.EncodeToString([]byte(svg))).Do(cdp.WithExecutor(browser, chromedp.FromContext(browser).Target))
				} else {
					err = fetch.FailRequest(request.RequestID, network.ErrorReasonBlockedByClient).Do(cdp.WithExecutor(browser, chromedp.FromContext(browser).Target))
				}
				_ = err
			}()
		}
	})
	var cycle float64
	err := chromedp.Run(browser, fetch.Enable(), chromedp.EmulateViewport(960, 640), chromedp.Navigate("https://pelican.invalid/scene.svg"), chromedp.Evaluate(`(() => {
const s=document.querySelector('svg');s.style.cssText+=';width:960px!important;height:640px!important;overflow:hidden!important;background:white!important';
s.pauseAnimations();const durations=[...s.querySelectorAll('animate,animateTransform,animateMotion')].map(a=>{try{return a.getSimpleDuration()}catch{return 0}});
document.getAnimations().forEach(a=>{a.pause();durations.push(a.effect.getTiming().duration/1000)});
const usable=durations.filter(v=>Number.isFinite(v)&&v>=.5&&v<=10);return usable.length?Math.min(...usable):3;
})()`, &cycle))
	if err != nil {
		return nil, nil, fmt.Errorf("render SVG: %w", err)
	}
	frames := make([]PelicanImage, 0, 4)
	sheet := image.NewRGBA(image.Rect(0, 0, 1920, 1280))
	for i, fraction := range []float64{0, .23, .51, .79} {
		seconds := cycle * fraction
		var data []byte
		err = chromedp.Run(browser, chromedp.Evaluate(fmt.Sprintf(`document.querySelector('svg').setCurrentTime(%f);document.getAnimations().forEach(a=>{a.pause();a.currentTime=%f});`, seconds, seconds*1000), nil), chromedp.CaptureScreenshot(&data))
		if err != nil {
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
