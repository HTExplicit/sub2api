//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

const systemPromptRemoteBootstrapMigration = "269_strip_removed_remote_rules_bootstrap_from_system_prompts.sql"

// TestMain applied every migration, 269 included, to an empty database. The
// test stores libraries made of invented prompt text, applies 269, checks that
// only the fenced route block and its fetch lines go, that a reference it does
// not recognise is left as stored, and applies it again.
func TestMigration269StripsRemovedRemoteRulesBootstrap(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	content, err := dbmigrations.FS.ReadFile(systemPromptRemoteBootstrapMigration)
	require.NoError(t, err)
	migration := string(content)

	const route = "https://example.test/skills/security-research/current"
	fence := "```text\nREMOTE_ROOT = " + route + "\n```"
	paragraphs := func(parts ...string) string { return strings.Join(parts, "\n\n") }
	inlineReference := "A rule that names " + route + "/ONE.md in the middle of a sentence."
	prompt := func(id, body string) map[string]any {
		return map[string]any{"id": id, "name": "Prompt " + id, "body": body, "position": "append", "role": "developer"}
	}
	library := func(prompts ...map[string]any) string {
		raw, err := json.Marshal(map[string]any{"enabled": true, "default_prompt_id": "bootstrap", "prompts": prompts})
		require.NoError(t, err)
		return string(raw)
	}
	store := func(value string) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at) VALUES ('system_prompts', $1, TIMESTAMPTZ '2000-01-01 00:00:00+00')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, value)
		require.NoError(t, err)
	}
	stored := func() (value string, untouched bool) {
		t.Helper()
		require.NoError(t, tx.QueryRowContext(ctx, `
SELECT value, updated_at = TIMESTAMPTZ '2000-01-01 00:00:00+00' FROM settings WHERE key = 'system_prompts'`).Scan(&value, &untouched))
		return value, untouched
	}

	before := library(
		prompt("bootstrap", paragraphs("Intro paragraph.", "# Working rules",
			"Before work, fetch and read these cloud files first, in order:", fence,
			"1. `REMOTE_ROOT/ONE.md`\n2. `REMOTE_ROOT/TWO.md`",
			"If a cloud file cannot be read, say so. See REMOTE_ROOT/ONE.md.", "Closing line.")),
		prompt("plain", paragraphs("Plain text with no route.", "Second paragraph.")),
		prompt("inline", paragraphs(inlineReference, "Tail.")),
		prompt("only-block", fence),
		prompt("block-at-end", paragraphs("Keep me.", "Fetch the Cloud files:", fence)),
		prompt("no-lead", paragraphs("Keep one.", fence, "Keep two.")),
	)
	after := library(
		prompt("bootstrap", paragraphs("Intro paragraph.", "# Working rules", "Closing line.")),
		prompt("plain", paragraphs("Plain text with no route.", "Second paragraph.")),
		prompt("inline", paragraphs(inlineReference, "Tail.")),
		prompt("only-block", fence),
		prompt("block-at-end", "Keep me."),
		prompt("no-lead", paragraphs("Keep one.", "Keep two.")),
	)

	store(before)
	for run := 1; run <= 2; run++ {
		_, err := tx.ExecContext(ctx, migration)
		require.NoError(t, err, "run %d", run)
		value, untouched := stored()
		require.JSONEq(t, after, value, "run %d", run)
		require.False(t, untouched, "run %d writes the changed library", run)
	}

	// A library that never named the route is not written, byte for byte.
	clean := library(prompt("bootstrap", paragraphs("Plain text with no route.", "Second paragraph.")))
	store(clean)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	value, untouched := stored()
	require.Equal(t, clean, value)
	require.True(t, untouched, "a library without the route is not written")

	// Text that is not a library is left as it is, and a missing row stays missing.
	for _, other := range []string{"not json", `["prompts"]`, `{"enabled": true}`, `{"prompts": "x"}`} {
		store(other)
		_, err = tx.ExecContext(ctx, migration)
		require.NoError(t, err, other)
		value, untouched = stored()
		require.Equal(t, other, value)
		require.True(t, untouched, other)
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = 'system_prompts'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, migration)
	require.NoError(t, err)
	var rows int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'system_prompts'`).Scan(&rows))
	require.Zero(t, rows)
}
