package service

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

const AccountTestPromptMaxCharacters = 8192

func accountTextTestOutputLimit(prompts ...string) int {
	for _, prompt := range prompts {
		if strings.TrimSpace(prompt) != "" {
			return 1024
		}
	}
	return 1
}

const accountTestPromptContextKey = "account_test_user_prompt"
const accountTestScheduledDefaultsContextKey = "account_test_scheduled_defaults"

func resolveAntigravityTestPrompt(prompts ...string) string {
	if len(prompts) == 0 {
		return "."
	}
	return resolveAccountTestPrompt(prompts...)
}

func ValidateAccountTextTestPrompt(prompt, model, mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "compact", "image", "video", "tts", "stt", "realtime":
		return nil
	}
	if strings.Contains(strings.ToLower(model), "image") {
		return nil
	}
	return ValidateAccountTestPrompt(prompt)
}

func ValidateAccountTestPrompt(prompt string) error {
	if !utf8.ValidString(prompt) || utf8.RuneCountInString(prompt) > AccountTestPromptMaxCharacters {
		return errors.New("test prompt must be valid UTF-8 and at most 8192 characters")
	}
	return nil
}

func resolveAccountTestPrompt(prompts ...string) string {
	if len(prompts) > 0 && strings.TrimSpace(prompts[0]) != "" {
		return prompts[0]
	}
	return "hi"
}

func accountTestUserPrompt(c *gin.Context) string {
	if c != nil {
		return resolveAccountTestPrompt(c.GetString(accountTestPromptContextKey))
	}
	return "hi"
}
