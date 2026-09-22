package service

import (
	"context"
	"fmt"
	"io/fs"
	"strings"

	registry "github.com/HTExplicit/sub2api-plugins/promptskills/registry"
	promptsource "github.com/HTExplicit/sub2api-plugins/promptskills/source"
)

const (
	BusinessSystemPromptManagedSourceGPT56 = promptsource.BusinessSystemPromptManagedSourceGPT56
	GPT56PromptLicenseSHA256               = promptsource.GPT56PromptLicenseSHA256
	gpt56PromptRepository                  = "MDX-Tom/gpt-5.6-instruct"
	remoteSkillExpectedFiles               = 458
	remoteSkillExpectedUpstreamFiles       = 457
	remoteSkillExpectedPinnedFiles         = 1
	remoteSkillPinnedWAFPath               = "skills/sec-assessment-tooling/pentest-tools/src-hunter/references/payloader/waf-bypass.md"
	remoteSkillPinnedWAFSHA256             = "0273517455962bb9908264f82e4708b31d541c91c2ec715e8032d6c1376728b5"
)

func readRemoteSkillTreeFS(tree fs.FS, root string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	err := fs.WalkDir(tree, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(name, root), "/")
		raw, readErr := fs.ReadFile(tree, name)
		if readErr != nil {
			return readErr
		}
		files[relative] = raw
		return nil
	})
	return files, err
}

var embeddedBusinessSystemPrompt = registry.DefaultPrompt()

type remoteSkillSeedFixtureFS struct{ fs.FS }

func (f remoteSkillSeedFixtureFS) Open(name string) (fs.File, error) {
	return f.FS.Open(strings.Replace(name, "remote_skill_seed/", "seed/", 1))
}

var remoteSkillSeedFS fs.FS = remoteSkillSeedFixtureFS{FS: registry.SeedFS()}

func validateRemoteSkillMarkdownClosure(name, content string, files map[string][]byte) error {
	if err := registry.ValidateMarkdownClosure(name, content, files); err != nil {
		return fmt.Errorf("%w: %v", ErrBusinessSystemPromptBundleInvalid, err)
	}
	return nil
}

func rewriteRemoteSkillPublishedFiles(files map[string][]byte) map[string][]byte {
	output, err := rewriteRemoteSkillPublishedFilesChecked(context.Background(), files)
	if err != nil {
		panic(err)
	}
	return output
}
func remoteSkillFileChanges(active *RemoteSkillCandidate, candidate RemoteSkillCandidate) []RemoteSkillFileChange {
	changes, err := remoteSkillFileChangesChecked(context.Background(), active, candidate)
	if err != nil {
		panic(err)
	}
	return changes
}
