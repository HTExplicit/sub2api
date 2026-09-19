package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/testextensions"
	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func init() { testextensions.Install() }

func enableImageStudioPolicy(t *testing.T) {
	t.Helper()
	testextensions.InstallImageTools(extensionv1.ImageToolsConfig{StudioEnabled: true})
	t.Cleanup(testextensions.Install)
}
