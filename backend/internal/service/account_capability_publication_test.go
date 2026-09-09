package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func publicationTestSnapshot() *CapabilityPublicationSnapshot {
	folderID := int64(9)
	account := &Account{ID: 7, Name: "source account", Platform: PlatformOpenAI, WirePlatform: PlatformOpenAI, Type: AccountTypeAPIKey, ManagementFolderID: &folderID, Status: StatusActive, Credentials: map[string]any{"api_key": "do-not-persist-this-secret", "base_url": "https://provider.invalid", "model_mapping": map[string]any{"private-model": "private-upstream"}}, Extra: map[string]any{"openai_responses_mode": "force_chat_completions"}}
	price := 0.01
	group := &Group{ID: 23, Name: "gpt", Platform: PlatformOpenAI, WirePlatform: PlatformOpenAI, RateMultiplier: 0.2, Status: StatusActive, ModelPricing: []ChannelModelPricing{{Models: []string{"gpt-6-astra"}, InputPrice: &price, OutputPrice: &price}}}
	now := time.Now()
	result, _ := json.Marshal(publicationProbeResult{Status: "alive", Protocol: "chat_completions", Profile: "text", UpstreamModel: "gpt-6-astra", RequestCount: 1})
	return &CapabilityPublicationSnapshot{Request: CapabilityPublicationRequest{Scope: CapabilityPublicationScope{FolderIDs: []int64{9}, AccountIDs: []int64{7}}, Groups: []CapabilityPublicationGroup{{ID: 23, Name: "gpt", Platform: PlatformOpenAI, RateMultiplier: 0.2, Models: []CapabilityPublicationModel{{PublicModel: "gpt-6-astra", EvidenceIDs: []int64{11}}}}}}, Accounts: map[int64]*CapabilityPublicationAccountSnapshot{7: {Account: account, Bindings: map[int64]int{55: 17}}}, Groups: map[int64]*CapabilityPublicationGroupSnapshot{23: {Group: group, Bindings: map[int64]int{}}}, Evidence: map[int64]CapabilityPublicationEvidence{11: {ID: 11, AccountID: 7, FolderID: &folderID, ConfigFingerprint: ManagedModelAccountFingerprint(account), UpstreamModel: "gpt-6-astra", Protocol: "chat_completions", Profile: "text", Status: "succeeded", RunKind: "probe", RunFolderIDs: []int64{9}, RunAccountIDs: []int64{7}, Result: result, FinishedAt: &now}}}
}

func TestCapabilityPublicationPreservesPrivateStateAndScopesSelectors(t *testing.T) {
	snap := publicationTestSnapshot()
	before, _ := json.Marshal(snap.Accounts[7].Account)
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Accounts) != 1 || len(plan.Groups) != 1 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	ap := plan.Accounts[0]
	if !reflect.DeepEqual(ap.AddGroupIDs, []int64{23}) || len(ap.RemoveGroupIDs) != 0 || ap.Schedulable == nil || !*ap.Schedulable {
		t.Fatalf("wrong scoped patch: %#v", ap)
	}
	selector := ManagedModelBranchSelector(23, "gpt-6-astra", PlatformOpenAI, "chat_completions", "gpt-6-astra")
	if !reflect.DeepEqual(ap.ModelMapping, map[string]string{selector: "gpt-6-astra"}) {
		t.Fatalf("private mapping leaked into patch: %#v", ap.ModelMapping)
	}
	if snap.Accounts[7].Bindings[55] != 17 {
		t.Fatal("private priority changed")
	}
	after, _ := json.Marshal(snap.Accounts[7].Account)
	if string(before) != string(after) {
		t.Fatal("preview mutated account")
	}
	if !reflect.DeepEqual(plan.Groups[0].ManagedModelRoutes.Routes[0].Endpoints, []string{"chat_completions", "messages", "responses"}) {
		t.Fatal("wire evidence was not compiled to adapter entrypoints")
	}
	b, _ := json.Marshal(plan)
	if strings.Contains(string(b), "do-not-persist-this-secret") {
		t.Fatal("plan contains credentials")
	}
	if ManagedModelSelector(23, "gpt-6-astra") == ManagedModelSelector(33, "gpt-6-astra") {
		t.Fatal("standard/VIP selectors overlap")
	}
	var saved CapabilityPublicationPlan
	if err = json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if !CapabilityPublicationPlansEqual(plan, &saved) {
		t.Fatal("saved JSON plan cannot be reapplied identically")
	}
}

func TestCapabilityPublicationRejectsInvalidEvidenceAndPrivateSemanticChanges(t *testing.T) {
	tests := []struct {
		name   string
		change func(*CapabilityPublicationSnapshot)
	}{
		{"credential changed", func(s *CapabilityPublicationSnapshot) { s.Accounts[7].Account.Credentials["api_key"] = "changed" }},
		{"folder moved", func(s *CapabilityPublicationSnapshot) {
			id := int64(10)
			s.Accounts[7].Account.ManagementFolderID = &id
		}},
		{"discovery is not liveness", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.RunKind = "discover"
			s.Evidence[11] = e
		}},
		{"superseded by later terminal failure", func(s *CapabilityPublicationSnapshot) { e := s.Evidence[11]; e.Superseded = true; s.Evidence[11] = e }},
		{"empty output is not alive", func(s *CapabilityPublicationSnapshot) { e := s.Evidence[11]; e.Status = "failed"; s.Evidence[11] = e }},
		{"unsupported actual wire", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.Protocol = "compact"
			e.Result = json.RawMessage(`{"status":"alive","protocol":"compact","profile":"text","upstream_model":"gpt-6-astra","request_count":1}`)
			s.Evidence[11] = e
		}},
		{"missing price", func(s *CapabilityPublicationSnapshot) { s.Groups[23].Group.ModelPricing = nil }},
		{"changed multiplier", func(s *CapabilityPublicationSnapshot) { s.Groups[23].Group.RateMultiplier = 0.4 }},
		{"empty private mapping", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Credentials["model_mapping"] = map[string]any{}
		}},
		{"wildcard private mapping", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Credentials["model_mapping"] = map[string]any{"*": "*"}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := publicationTestSnapshot()
			tt.change(snap)
			if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
				t.Fatal("unsafe evidence was accepted")
			}
		})
	}
}

func TestCapabilityPublicationOutOfScopeOnlyExplicitDetachment(t *testing.T) {
	snap := publicationTestSnapshot()
	outside := &Account{ID: 99, Name: "outside", Platform: PlatformOpenAI, Schedulable: true}
	snap.Accounts[99] = &CapabilityPublicationAccountSnapshot{Account: outside, Bindings: map[int64]int{23: 7, 88: 12}}
	snap.Groups[23].Bindings[99] = 7
	initial, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, ap := range initial.Accounts {
		if ap.AccountID == 99 {
			t.Fatal("merge modified an unselected outside account")
		}
	}
	snap.Request.DetachAccountIDs = []int64{99}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, ap := range plan.Accounts {
		if ap.AccountID == 99 {
			if ap.Schedulable != nil || len(ap.ModelMapping) > 0 || len(ap.RemoveSelectors) > 0 || !reflect.DeepEqual(ap.RemoveGroupIDs, []int64{23}) {
				t.Fatalf("outside account mutated beyond detachment: %#v", ap)
			}
		}
	}
}

func TestCapabilityPublicationSchedulingRequiresTerminalAccountEvidence(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "model failure", true: "terminal credential failure"}[terminal], func(t *testing.T) {
			snap := publicationTestSnapshot()
			snap.Request.Groups = nil
			snap.Request.SchedulingEvidenceIDs = []int64{11}
			snap.Accounts[7].Account.Schedulable = true
			e := snap.Evidence[11]
			e.Status = "failed"
			e.Result, _ = json.Marshal(publicationProbeResult{Status: "failed", Classification: "credential_invalid", AccountFailure: terminal, Protocol: e.Protocol, Profile: e.Profile, UpstreamModel: e.UpstreamModel, RequestCount: 1})
			snap.Evidence[11] = e
			plan, err := (&AccountCapabilityPublicationService{}).build(snap)
			if !terminal {
				if err == nil {
					t.Fatal("failed model disabled entire account")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Accounts) != 1 || plan.Accounts[0].Schedulable == nil || *plan.Accounts[0].Schedulable {
				t.Fatal("terminal credential failure did not propose disable")
			}
			if !snap.Accounts[7].Account.Schedulable {
				t.Fatal("preview changed scheduling")
			}
		})
	}
}

func TestCapabilityPublicationCountMetadataOnlySupplementsInference(t *testing.T) {
	snap := publicationTestSnapshot()
	e := snap.Evidence[11]
	e.ID = 12
	e.Protocol = "responses_input_tokens"
	e.Result = json.RawMessage(`{"status":"available","classification":"metadata_available","protocol":"responses_input_tokens","profile":"text","upstream_model":"gpt-6-astra","request_count":1}`)
	snap.Evidence[12] = e
	snap.Request.Groups[0].Models[0].EvidenceIDs = []int64{12}
	if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
		t.Fatal("token count created a live inference route")
	}
	snap.Request.Groups[0].Models[0].EvidenceIDs = []int64{11, 12}
	plan, err := (&AccountCapabilityPublicationService{}).build(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !publicationHasString(ManagedModelRouteBranches(plan.Groups[0].ManagedModelRoutes.Routes[0])[0].Accounts[0].Endpoints, "count_tokens") {
		t.Fatal("valid metadata proof not attached")
	}
}

func TestCapabilityPublicationIngressCompilation(t *testing.T) {
	tests := []struct {
		name, platform, mode, protocol string
		want                           []string
	}{
		{"confirmed responses", PlatformOpenAI, "force_responses", "responses", []string{"chat_completions", "messages", "responses"}},
		{"managed responses pins wire without hidden Chat fallback", PlatformOpenAI, "", "responses", []string{"chat_completions", "messages", "responses"}},
		{"chat wire bridge", PlatformOpenAI, "force_chat_completions", "chat_completions", []string{"chat_completions", "messages", "responses"}},
		{"verified alternate wire does not change ordinary default", PlatformOpenAI, "force_chat_completions", "responses", []string{"chat_completions", "messages", "responses"}},
		{"native messages bridge", PlatformAnthropic, "", "messages", []string{"chat_completions", "messages", "responses"}},
		{"WS is independent", PlatformOpenAI, "force_responses", "responses_websocket", []string{"responses_websocket"}},
		{"unknown protocol", PlatformOpenAI, "force_responses", "compact", nil},
		{"wrong metadata endpoint", PlatformAnthropic, "", "responses_input_tokens", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Account{Platform: tt.platform, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_responses_mode": tt.mode}}
			if got := CapabilityIngressEndpoints(a, tt.protocol); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
}

func TestCapabilityPublicationInputRejectsAmbiguousAliases(t *testing.T) {
	req := publicationTestSnapshot().Request
	if err := validateCapabilityPublicationRequest(req); err != nil {
		t.Fatal(err)
	}
	req.Groups[0].Models[0].Aliases = []string{"GPT-6-ASTRA"}
	if err := validateCapabilityPublicationRequest(req); err == nil {
		t.Fatal("case equivalent alias collided")
	}
	req = publicationTestSnapshot().Request
	req.Groups[0].ID = 0
	if err := validateCapabilityPublicationRequest(req); err == nil {
		t.Fatal("arbitrary new group accepted")
	}
}

func TestCapabilityPublicationUnprobedRemovalStillRequiresFolderScope(t *testing.T) {
	snap := publicationTestSnapshot()
	snap.Request.Groups[0].Models = nil
	snap.Groups[23].Bindings[7] = 29
	outsideFolder := int64(99)
	snap.Accounts[7].Account.ManagementFolderID = &outsideFolder
	if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
		t.Fatal("unprobed out-of-folder account was allowed to lose public bindings")
	}
}

func TestCapabilityPublicationDiscoverySchedulingEvidence(t *testing.T) {
	makeDiscovery := func() *CapabilityPublicationSnapshot {
		snap := publicationTestSnapshot()
		snap.Request.Groups = nil
		snap.Request.SchedulingEvidenceIDs = []int64{11}
		snap.Accounts[7].Account.Schedulable = true
		e := snap.Evidence[11]
		e.RunKind, e.Status, e.UpstreamModel, e.Protocol, e.Profile = "discover", "failed", "", "", ""
		e.Result, _ = json.Marshal(AccountCapabilityDiscoveryResult{Status: "failed", Classification: "credential_invalid", HTTPStatus: 401, ErrorCode: "invalid_api_key", RequestCount: 1, AccountFailure: true, Source: "upstream"})
		snap.Evidence[11] = e
		return snap
	}
	t.Run("explicit catalog credential failure can only disable scheduling", func(t *testing.T) {
		snap := makeDiscovery()
		plan, err := (&AccountCapabilityPublicationService{}).build(snap)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Groups) != 0 || len(plan.Accounts) != 1 || plan.Accounts[0].Schedulable == nil || *plan.Accounts[0].Schedulable || len(plan.Accounts[0].ModelMapping) != 0 {
			t.Fatalf("discovery escaped its scheduling-only boundary: %#v", plan)
		}
		if !snap.Accounts[7].Account.Schedulable {
			t.Fatal("preview disabled account before apply")
		}
		if _, _, err = publicationValidateEvidence(snap, 11); err == nil {
			t.Fatal("catalog failure became model evidence")
		}
	})
	t.Run("historical unchanged credential failure retains its recorded meaning", func(t *testing.T) {
		snap := makeDiscovery()
		e := snap.Evidence[11]
		old := time.Now().Add(-25 * time.Hour)
		e.FinishedAt = &old
		snap.Evidence[11] = e
		plan, err := (&AccountCapabilityPublicationService{}).build(snap)
		if err != nil || len(plan.Accounts) != 1 || plan.Accounts[0].Schedulable == nil || *plan.Accounts[0].Schedulable {
			t.Fatalf("unchanged, unsuperseded evidence was expired solely by age: plan=%#v err=%v", plan, err)
		}
	})
	for _, test := range []struct {
		name string
		edit func(*CapabilityPublicationSnapshot)
	}{
		{"bare 401", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.Result = json.RawMessage(`{"status":"failed","classification":"auth_failed","http_status":401,"request_count":1,"account_failure":false,"source":"upstream"}`)
			s.Evidence[11] = e
		}},
		{"success directory is not liveness", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.Status = "succeeded"
			e.Result = json.RawMessage(`{"status":"discovered","classification":"catalog_discovered","http_status":200,"request_count":1,"account_failure":false,"source":"upstream"}`)
			s.Evidence[11] = e
		}},
		{"credential configuration changed", func(s *CapabilityPublicationSnapshot) {
			s.Accounts[7].Account.Credentials["api_key"] = "new-credential"
		}},
		{"superseded by auth recovery", func(s *CapabilityPublicationSnapshot) { e := s.Evidence[11]; e.Superseded = true; s.Evidence[11] = e }},
		{"outside discovery scope", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.RunFolderIDs = []int64{999}
			s.Evidence[11] = e
		}},
		{"never dispatched", func(s *CapabilityPublicationSnapshot) {
			e := s.Evidence[11]
			e.Result = json.RawMessage(`{"status":"failed","classification":"credential_invalid","http_status":401,"request_count":0,"account_failure":true,"source":"upstream"}`)
			s.Evidence[11] = e
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			snap := makeDiscovery()
			test.edit(snap)
			if _, err := (&AccountCapabilityPublicationService{}).build(snap); err == nil {
				t.Fatal("unproven discovery disabled account")
			}
		})
	}
}
