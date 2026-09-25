package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type unavailableIndependentPromptStore struct {
	BusinessSystemPromptStore
	IndependentPromptStore
	err error
}

func (s *unavailableIndependentPromptStore) InitializeIndependentPrompts(context.Context, RemoteSkillRegistryFiles) (map[string][]byte, error) {
	return nil, s.err
}

func TestPromptProviderRejectsIncompleteMigrationBeforeServing(t *testing.T) {
	failure := errors.New("active publication is incomplete")
	store := &unavailableIndependentPromptStore{err: failure}
	frozen := ProvideFrozenPromptFiles(store, nil)
	prompts, err := ProvideBusinessSystemPromptService(store, nil, frozen, nil, nil, nil)
	require.ErrorIs(t, err, failure)
	require.Nil(t, prompts)
	_, err = frozen.LoadPublishedFile(context.Background(), "SKILL.md")
	require.ErrorIs(t, err, ErrBusinessSystemPromptUnavailable)
}
