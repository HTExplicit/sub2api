package main

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRunCodexContextContractVerification(t *testing.T) {
	var output bytes.Buffer
	require.Equal(t, 0, runCodexContextContractVerification(&output, false, false, nil))
	require.Equal(t, service.OfficialCodexContextContractSuccessLine+"\n", output.String())
}

func TestRunCodexContextContractVerificationFailsWithFixedShape(t *testing.T) {
	var output bytes.Buffer
	exitCode := runCodexContextContractVerificationWith(
		&output,
		false,
		false,
		nil,
		func() (string, error) { return "sensitive-detail", errors.New("sensitive-detail") },
	)
	require.Equal(t, 1, exitCode)
	require.Equal(t, service.OfficialCodexContextContractFailureLine+"\n", output.String())
	require.NotContains(t, output.String(), "sensitive-detail")
}

func TestRunCodexContextContractVerificationRejectsConflictingArguments(t *testing.T) {
	tests := []struct {
		name           string
		setup, version bool
		args           []string
	}{
		{name: "setup", setup: true},
		{name: "version", version: true},
		{name: "positional", args: []string{"unexpected"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			require.Equal(t, 2, runCodexContextContractVerification(
				&output,
				test.setup,
				test.version,
				test.args,
			))
			require.Equal(t, service.OfficialCodexContextInvalidArgsLine+"\n", output.String())
		})
	}
}
