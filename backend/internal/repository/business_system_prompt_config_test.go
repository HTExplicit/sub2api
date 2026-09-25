package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/nativeapi"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func promptConfigFixtureRule(id string, templateID, versionID int64) nativeapi.PromptRule {
	return nativeapi.PromptRule{ID: id, Name: id, Enabled: true, TemplateID: templateID, VersionID: versionID,
		Role: "auto", Platforms: []string{"openai"}, Position: "control_append", ModelMatch: "upstream", Models: []string{}}
}

func promptConfigFixturePolicy(rules ...nativeapi.PromptRule) nativeapi.PromptRulePolicy {
	ids := make([]string, 0, len(rules))
	for _, rule := range rules {
		ids = append(ids, rule.ID)
	}
	return nativeapi.PromptRulePolicy{Version: 2, Rules: rules, DefaultRuleIDs: ids}
}

func promptConfigExpectPrevious(t *testing.T, mock sqlmock.Sqlmock, policy nativeapi.PromptRulePolicy) {
	t.Helper()
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	mock.ExpectQuery("SELECT policy FROM system_prompt_rule_policies").WillReturnRows(sqlmock.NewRows([]string{"policy"}).AddRow(raw))
}

func promptConfigExpectVersion(t *testing.T, mock sqlmock.Sqlmock, managed any, body string, templateID, versionID int64) {
	t.Helper()
	hash, size, err := service.ValidateBusinessSystemPromptBody(body)
	require.NoError(t, err)
	mock.ExpectQuery("SELECT t.managed_source, v.body").WithArgs(templateID, versionID).WillReturnRows(
		sqlmock.NewRows([]string{"managed_source", "body", "sha256", "byte_length", "composition_mode", "bundle_id", "bundle_manifest_sha256"}).
			AddRow(managed, body, hash, size, "inline", nil, nil))
}

type promptConfigPolicyMatcher struct {
	versionByRule map[string]int64
	newTemplateID int64
}

func (m promptConfigPolicyMatcher) Match(value driver.Value) bool {
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var policy nativeapi.PromptRulePolicy
	if json.Unmarshal([]byte(raw), &policy) != nil || policy.Version != 2 || len(policy.Rules) != len(m.versionByRule) {
		return false
	}
	for _, rule := range policy.Rules {
		if rule.VersionID != m.versionByRule[rule.ID] || rule.FollowActive || rule.Delivery != "" {
			return false
		}
		if rule.ID == "new" && rule.TemplateID != m.newTemplateID {
			return false
		}
	}
	return len(policy.DefaultRuleIDs) == 1 && policy.DefaultRuleIDs[0] == "new"
}

func TestPromptConfigAtomicSavePreservesOtherPinnedRules(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	first := promptConfigFixtureRule("edited", 2, 5)
	second := promptConfigFixtureRule("shared", 2, 5)
	third := promptConfigFixtureRule("new", 0, 0)
	previous := promptConfigFixturePolicy(first, second)
	input := service.PromptConfigUpdate{ExpectedRevision: 7, Enabled: true, ExposeServerPrompt: true,
		Policy:   promptConfigFixturePolicy(first, second, third),
		Contents: map[string]service.PromptContentDraft{"edited": {Body: "edited content"}, "new": {Body: "new content"}}}
	input.Policy.DefaultRuleIDs = []string{"new"}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT revision FROM system_prompt_runtime").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(7))
	promptConfigExpectPrevious(t, mock, previous)
	mock.ExpectQuery("SELECT EXISTS").WithArgs(`["edited","shared","new"]`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	promptConfigExpectVersion(t, mock, nil, "original content", 2, 5)
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(version\\), 0\\)").WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(4))
	hash, size, err := service.ValidateBusinessSystemPromptBody("edited content")
	require.NoError(t, err)
	mock.ExpectQuery("INSERT INTO system_prompt_template_versions").WithArgs(int64(2), int64(5), "edited content", hash, size, "inline", nil, nil, int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(99))
	promptConfigExpectVersion(t, mock, nil, "original content", 2, 5)
	mock.ExpectExec("UPDATE system_prompt_template_versions").WithArgs(int64(5), int64(2), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("INSERT INTO system_prompt_templates").WithArgs(sqlmock.AnyArg(), "new", int64(9)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(version\\), 0\\)").WithArgs(int64(3)).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(0))
	hash, size, err = service.ValidateBusinessSystemPromptBody("new content")
	require.NoError(t, err)
	mock.ExpectQuery("INSERT INTO system_prompt_template_versions").WithArgs(int64(3), int64(1), "new content", hash, size, "inline", nil, nil, int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mock.ExpectExec("UPDATE system_prompt_rule_policies").WithArgs(promptConfigPolicyMatcher{map[string]int64{"edited": 99, "shared": 5, "new": 100}, 3}).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE system_prompt_runtime").WithArgs(true, true, false, int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	repo := &businessSystemPromptRepository{db: db}
	require.NoError(t, repo.SavePromptConfig(context.Background(), input, 9))
	require.Equal(t, int64(5), input.Policy.Rules[0].VersionID, "saving must not mutate the editor's previous snapshot")
	require.Zero(t, input.Policy.Rules[2].TemplateID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptConfigRejectsConflictDanglingManagedAndSourceDrift(t *testing.T) {
	for _, scenario := range []string{"revision", "referenced", "managed", "source"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			previous := promptConfigFixturePolicy(promptConfigFixtureRule("one", 2, 5))
			input := service.PromptConfigUpdate{ExpectedRevision: 7, Enabled: true, Policy: promptConfigFixturePolicy(promptConfigFixtureRule("one", 2, 5)),
				Contents: map[string]service.PromptContentDraft{"one": {Body: "edited content"}}}
			if scenario == "source" {
				input.Policy.Rules[0].TemplateID = 3
			}
			mock.ExpectBegin()
			revision := int64(7)
			if scenario == "revision" {
				revision++
			}
			mock.ExpectQuery("SELECT revision FROM system_prompt_runtime").WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(revision))
			if scenario != "revision" {
				promptConfigExpectPrevious(t, mock, previous)
				mock.ExpectQuery("SELECT EXISTS").WithArgs(`["one"]`).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(scenario == "referenced"))
				if scenario == "managed" {
					promptConfigExpectVersion(t, mock, "github-seed", "managed source body", 2, 5)
				}
			}
			mock.ExpectRollback()
			err = (&businessSystemPromptRepository{db: db}).SavePromptConfig(context.Background(), input, 9)
			switch scenario {
			case "revision":
				require.ErrorIs(t, err, service.ErrBusinessSystemPromptRevisionConflict)
			case "referenced":
				require.ErrorIs(t, err, service.ErrPromptRuleReferenced)
			case "managed":
				require.ErrorIs(t, err, service.ErrBusinessSystemPromptSourceNotManaged)
			case "source":
				require.ErrorIs(t, err, service.ErrBusinessSystemPromptInvalid)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestPromptConfigV2SnapshotDoesNotReadGlobalContent(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	policy := promptConfigFixturePolicy(promptConfigFixtureRule("one", 2, 5))
	raw, err := json.Marshal(policy)
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT r.enabled, r.expose_server_prompt, r.compact_enabled").WillReturnRows(
		sqlmock.NewRows([]string{"enabled", "expose_server_prompt", "compact_enabled", "revision", "updated_at", "policy"}).AddRow(true, false, true, 7, time.Now(), raw))
	mock.ExpectCommit()
	snapshot, err := (&businessSystemPromptRepository{db: db}).LoadBusinessSystemPromptRules(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(7), snapshot.Revision)
	require.Equal(t, policy, *snapshot.RulePolicy)
	require.Empty(t, snapshot.Body)
	require.Zero(t, snapshot.TemplateID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPromptConfigContentLimitRejectsBeforeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	input := service.PromptConfigUpdate{ExpectedRevision: 7, Policy: promptConfigFixturePolicy(promptConfigFixtureRule("new", 0, 0)),
		Contents: map[string]service.PromptContentDraft{"new": {Body: strings.Repeat("x", 65537)}}}
	err = (&businessSystemPromptRepository{db: db}).SavePromptConfig(context.Background(), input, 9)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
