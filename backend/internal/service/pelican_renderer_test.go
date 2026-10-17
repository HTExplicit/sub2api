//go:build unit

package service

import (
	"bytes"
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPelicanChromiumRenderer(t *testing.T) {
	if os.Getenv("PELICAN_RENDER_TEST") != "1" {
		t.Skip("executed once inside the candidate runtime image")
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 960 640"><rect width="960" height="640" fill="#e5f4ef"/><circle cy="320" r="80" fill="#da9c45"><animate attributeName="cx" values="100;800;100" dur="2s" repeatCount="indefinite"/></circle><text x="700" y="600">FIXTURE</text></svg>`
	frames, sheet, err := (&pelicanChromiumRenderer{}).Render(context.Background(), svg)
	require.NoError(t, err)
	require.Len(t, frames, 4)
	for i, expected := range []float64{0, .46, 1.02, 1.58} {
		require.InDelta(t, expected, frames[i].Time, .001)
		img, decodeErr := png.Decode(bytes.NewReader(frames[i].PNG))
		require.NoError(t, decodeErr)
		require.Equal(t, 960, img.Bounds().Dx())
		require.Equal(t, 640, img.Bounds().Dy())
	}
	require.NotEqual(t, frames[0].PNG, frames[1].PNG, "sampling must capture motion rather than repeat one screenshot")
	img, err := png.Decode(bytes.NewReader(sheet))
	require.NoError(t, err)
	require.Equal(t, 1920, img.Bounds().Dx())
	require.Equal(t, 1280, img.Bounds().Dy())
	processes, err := os.ReadDir("/proc")
	require.NoError(t, err)
	for _, process := range processes {
		command, _ := os.ReadFile(filepath.Join("/proc", process.Name(), "cmdline"))
		first := strings.SplitN(string(command), "\x00", 2)[0]
		require.False(t, strings.Contains(filepath.Base(first), "chromium"), "renderer leaked process %s", process.Name())
	}
	peak, err := os.ReadFile("/sys/fs/cgroup/memory.peak")
	if err == nil {
		t.Logf("candidate cgroup memory peak: %s bytes", strings.TrimSpace(string(peak)))
	}
}
