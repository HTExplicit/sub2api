package handler

import (
	"testing"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/testextensions"
)

func init() { testextensions.Install() }

func enableImageStudioPolicy(t *testing.T) {
	t.Helper()
	testextensions.InstallImageTools(extensionv1.ImageToolsConfig{StudioEnabled: true})
	t.Cleanup(testextensions.Install)
}
