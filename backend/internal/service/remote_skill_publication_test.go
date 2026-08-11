package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRemoteSkillPublicationKeepsPromptAndFilesInOneAtomicSnapshot(t *testing.T) {
	svc := &RemoteSkillRegistryService{}
	first := RemoteSkillPublication{
		Revision:              7,
		CandidateID:           41,
		EffectiveTreeSHA256:   hashBusinessSystemPromptBundleBytes([]byte("tree-one")),
		EffectivePromptSHA256: hashBusinessSystemPromptBundleBytes([]byte("prompt-one")),
		EffectivePromptBody:   "prompt-one",
		Files: map[string][]byte{
			"SKILL.md": []byte("tree-one"),
		},
	}
	require.NoError(t, svc.installPublication(first))

	published, err := svc.ActivePublication(context.Background())
	require.NoError(t, err)
	file, err := svc.LoadPublishedFile(context.Background(), "SKILL.md")
	require.NoError(t, err)
	require.Equal(t, int64(7), published.Revision)
	require.Equal(t, published.Revision, file.Revision)
	require.Equal(t, published.CandidateID, file.CandidateID)
	require.Equal(t, published.EffectiveTreeSHA256, file.EffectiveTreeSHA256)
	require.Equal(t, published.EffectivePromptSHA256, file.EffectivePromptSHA256)
	require.Equal(t, "prompt-one", published.EffectivePromptBody)
	require.Equal(t, []byte("tree-one"), file.Body)
	require.Equal(t, `"`+hashBusinessSystemPromptBundleBytes(file.Body)+`"`, file.ETag)
	require.Equal(t, "text/markdown; charset=utf-8", file.ContentType)

	first.Files["SKILL.md"][0] = 'X'
	first.EffectivePromptBody = "mutated"
	again, err := svc.ActivePublication(context.Background())
	require.NoError(t, err)
	againFile, err := svc.LoadPublishedFile(context.Background(), "SKILL.md")
	require.NoError(t, err)
	require.Equal(t, "prompt-one", again.EffectivePromptBody)
	require.Equal(t, []byte("tree-one"), againFile.Body)

	second := RemoteSkillPublication{
		Revision:              8,
		CandidateID:           42,
		EffectiveTreeSHA256:   hashBusinessSystemPromptBundleBytes([]byte("tree-two")),
		EffectivePromptSHA256: hashBusinessSystemPromptBundleBytes([]byte("prompt-two")),
		EffectivePromptBody:   "prompt-two",
		Files:                 map[string][]byte{"SKILL.md": []byte("tree-two")},
	}
	require.NoError(t, svc.installPublication(second))
	current, err := svc.ActivePublication(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(8), current.Revision)
	require.Equal(t, "prompt-two", current.EffectivePromptBody)
	require.Equal(t, "prompt-one", published.EffectivePromptBody)
}

func TestRemoteSkillPublicationRejectsInvalidIdentityAndPublicPaths(t *testing.T) {
	svc := &RemoteSkillRegistryService{}
	invalid := RemoteSkillPublication{
		Revision:              1,
		CandidateID:           1,
		EffectiveTreeSHA256:   hashBusinessSystemPromptBundleBytes([]byte("tree")),
		EffectivePromptSHA256: hashBusinessSystemPromptBundleBytes([]byte("different")),
		EffectivePromptBody:   "prompt",
		Files:                 map[string][]byte{"SKILL.md": []byte("tree")},
	}
	require.ErrorIs(t, svc.installPublication(invalid), ErrBusinessSystemPromptBundleInvalid)

	valid := invalid
	valid.EffectivePromptSHA256 = hashBusinessSystemPromptBundleBytes([]byte(valid.EffectivePromptBody))
	require.NoError(t, svc.installPublication(valid))
	for _, name := range []string{"", ".", "../SKILL.md", "references/../SKILL.md", "/SKILL.md", "references/", "missing.md", "SKILL.md?x=1"} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.LoadPublishedFile(context.Background(), name)
			require.Error(t, err)
		})
	}
}
