package source

import (
	_ "embed"
	"net/http"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

const BusinessSystemPromptMaxBytes = 64 << 10

type RemoteSkillHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func ValidateBusinessSystemPromptBody(body string) (string, int, error) {
	return extensionv1.ValidateTextDocument(body, BusinessSystemPromptMaxBytes)
}
func isLowerHexSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

//go:embed prompts/gpt_5_6_instruct_v45.md
var defaultGPT56Prompt string

func DefaultSeed() extensionv1.PromptSeed {
	return extensionv1.PromptSeed{Slug: "gpt_5_6_instruct", Name: "GPT-5.6 Instruct v45", Description: "MDX-Tom/gpt-5.6-instruct 的内置可选提示词。", ManagedSource: BusinessSystemPromptManagedSourceGPT56, Body: defaultGPT56Prompt, Note: "Imported from MDX-Tom/gpt-5.6-instruct v45", CompositionMode: "inline", SourceRepository: gpt56PromptRepository, SourceCommit: "77e7a649903f9556f2d7bfa0223fa99e123aad52", SourceVersion: "v45", SourceArtifact: "gpt-5.6-sol-unrestricted-v45.zip", SourceArtifactSHA256: "c86c2c6d20a4d1155d87422f485eb37b77539132270918c002b5d8237a5adf54", SourceLicenseSHA256: GPT56PromptLicenseSHA256}
}
