//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanChromiumRenderer(t *testing.T) {
	if os.Getenv("PELICAN_RENDER_TEST") != "1" {
		t.Skip("executed once inside the candidate runtime image")
	}
	const svg = "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"960\" height=\"640\" viewBox=\"0 0 960 640\"><rect width=\"960\" height=\"640\" fill=\"#e5f4ef\"/><circle cy=\"320\" r=\"80\" fill=\"#da9c45\"><animate attributeName=\"cx\" values=\"100;800;100\" dur=\"2s\" repeatCount=\"indefinite\"/></circle></svg>"
	const css = "<!doctype html><html><head><style>body{margin:0}@keyframes move{from{transform:translateX(0)}to{transform:translateX(600px)}}circle{animation:move 2s linear infinite}</style></head><body><svg width=\"960\" height=\"640\"><circle cx=\"100\" cy=\"320\" r=\"70\" fill=\"blue\"/></svg></body></html>"
	const script = "<html><body style=\"margin:0\"><svg width=\"960\" height=\"640\"><circle id=\"bird\" cy=\"320\" r=\"80\" fill=\"green\"/></svg><script>const start=performance.now();function frame(){document.getElementById('bird').setAttribute('cx',100+((performance.now()-start)/5)%600);requestAnimationFrame(frame)}requestAnimationFrame(frame)</script></body></html>"
	for _, tc := range []struct {
		name, source string
		realTime     bool
	}{{"SMIL", svg, false}, {"HTML CSS", css, false}, {"HTML JavaScript", script, true}} {
		t.Run(tc.name, func(t *testing.T) {
			frames, sheet, err := (&pelicanChromiumRenderer{}).Render(context.Background(), tc.source)
			require.NoError(t, err)
			require.Len(t, frames, 4)
			for i, fraction := range []float64{0, .23, .51, .79} {
				if tc.realTime {
					require.InDelta(t, 3*fraction, frames[i].Time, .5)
				} else {
					require.InDelta(t, 2*fraction, frames[i].Time, .001)
				}
				img, err := png.Decode(bytes.NewReader(frames[i].PNG))
				require.NoError(t, err)
				require.Equal(t, 960, img.Bounds().Dx())
				require.Equal(t, 640, img.Bounds().Dy())
				if i > 0 {
					require.NotEqual(t, frames[i-1].PNG, frames[i].PNG, "consecutive frames must capture motion")
					require.Greater(t, frames[i].Time, frames[i-1].Time)
				}
			}
			img, err := png.Decode(bytes.NewReader(sheet))
			require.NoError(t, err)
			require.Equal(t, 1920, img.Bounds().Dx())
			require.Equal(t, 1280, img.Bounds().Dy())
			requirePelicanChromiumStopped(t)
		})
	}
	t.Run("external resources blocked", func(t *testing.T) {
		var requests atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = w.Write([]byte("document.body.style.background='red'"))
		}))
		defer server.Close()
		source := fmt.Sprintf("<html><head><script src=\"%s/script.js\"></script></head><body style=\"margin:0;background:white\"><img src=\"%s/image\" style=\"display:none\"><svg width=\"960\" height=\"640\"/><script>fetch('%s/fetch').catch(()=>{})</script></body></html>", server.URL, server.URL, server.URL)
		frames, _, err := (&pelicanChromiumRenderer{}).Render(context.Background(), source)
		require.NoError(t, err)
		require.Zero(t, requests.Load())
		img, err := png.Decode(bytes.NewReader(frames[0].PNG))
		require.NoError(t, err)
		require.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, color.RGBAModel.Convert(img.At(400, 300)), "external script must not paint the work")
		requirePelicanChromiumStopped(t)
	})
	for name, source := range map[string]string{
		"inline nested document blocked":         "<html><body><svg/><iframe srcdoc=\"<html><body style='background:red'></body></html>\"></iframe></body></html>",
		"script-created nested document blocked": "<html><body><svg/><script>const f=document.createElement('iframe');f.srcdoc='<html><body>nested</body></html>';document.body.append(f)</script></body></html>",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := (&pelicanChromiumRenderer{}).Render(context.Background(), source)
			require.ErrorContains(t, err, "nested documents blocked")
			requirePelicanChromiumStopped(t)
		})
	}
	t.Run("page navigation blocked", func(t *testing.T) {
		_, _, err := (&pelicanChromiumRenderer{}).Render(context.Background(), "<html><body><svg/><script>setTimeout(()=>{location.href='https://pelican.invalid/escape'},100)</script></body></html>")
		require.ErrorContains(t, err, "navigation blocked")
		requirePelicanChromiumStopped(t)
	})
	peak, err := os.ReadFile("/sys/fs/cgroup/memory.peak")
	if err == nil {
		t.Logf("candidate cgroup memory peak: %s bytes", strings.TrimSpace(string(peak)))
	}
}

func requirePelicanChromiumStopped(t *testing.T) {
	t.Helper()
	processes, err := os.ReadDir("/proc")
	require.NoError(t, err)
	for _, process := range processes {
		command, _ := os.ReadFile(filepath.Join("/proc", process.Name(), "cmdline"))
		first := strings.SplitN(string(command), "\x00", 2)[0]
		require.False(t, strings.Contains(filepath.Base(first), "chromium"), "renderer leaked process %s", process.Name())
	}
}
