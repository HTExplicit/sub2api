package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type dataImportTaxonomySelection struct {
	FolderName *string
	TagNames   *[]string
}

type dataImportTaxonomyResolver struct {
	service accountConsoleAdminService
	folders map[string]service.AccountManagementFolder
	tags    map[string]service.AccountManagementTag
}

type dataImportGroupResolver struct {
	byName map[string][]service.Group
}

func validateDataImportRequest(req DataImportRequest) error {
	if err := validateDataImportUniformSettings(req.UniformSettings); err != nil {
		return err
	}
	return nil
}

func validateDataImportUniformSettings(settings DataImportUniformSettings) error {
	if settings.Notes != nil {
		if err := validateDataImportNotes(*settings.Notes); err != nil {
			return err
		}
	}
	if settings.ManagementFolder != nil {
		if err := validateDataTaxonomyName(*settings.ManagementFolder, true); err != nil {
			return fmt.Errorf("management_folder: %w", err)
		}
	}
	if settings.Tags != nil {
		if err := validateDataImportTags(*settings.Tags); err != nil {
			return err
		}
	}
	return validateDataImportOperationalSettings(settings.GroupIDs, settings.ProxyID, settings.Concurrency, settings.Priority, settings.RateMultiplier, settings.Status)
}

func validateDataImportNotes(setting DataImportNotesSetting) error {
	switch setting.Mode {
	case "append", "replace":
		return nil
	default:
		return fmt.Errorf("notes mode must be append or replace")
	}
}

func validateDataImportTags(tags []string) error {
	for _, tag := range tags {
		if err := validateDataTaxonomyName(tag, false); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
	}
	return nil
}

func validateDataImportOperationalSettings(groupIDs *[]int64, proxyID *int64, concurrency, priority *int, rateMultiplier *float64, status *string) error {
	if groupIDs != nil {
		for _, id := range *groupIDs {
			if id <= 0 {
				return errors.New("group_ids must contain only positive IDs")
			}
		}
	}
	if proxyID != nil && *proxyID < 0 {
		return errors.New("proxy_id must be zero or a positive ID")
	}
	if concurrency != nil && *concurrency < 0 {
		return errors.New("concurrency must be >= 0")
	}
	if priority != nil && *priority < 0 {
		return errors.New("priority must be >= 0")
	}
	if rateMultiplier != nil && *rateMultiplier < 0 {
		return errors.New("rate_multiplier must be >= 0")
	}
	if status != nil {
		if _, err := normalizeDataAccountStatus(*status); err != nil {
			return err
		}
	}
	return nil
}

func (h *AccountHandler) importData(ctx context.Context, req DataImportRequest) (DataImportResult, error) {
	result := DataImportResult{AccountIDs: []int64{}, Items: []DataImportItemResult{}}
	if err := validateDataImportRequest(req); err != nil {
		return result, err
	}

	proxyKeyToID, err := h.importDataProxies(ctx, req.Data.Proxies, &result)
	if err != nil {
		return result, err
	}
	existingAccounts, err := h.listAccountsFiltered(ctx, "", "", "", "", 0, "", "name", "asc")
	if err != nil {
		return result, err
	}
	identityIndex := buildDataIdentityIndex(existingAccounts)
	accountsByID := make(map[int64]service.Account, len(existingAccounts))
	for _, account := range existingAccounts {
		accountsByID[account.ID] = account
	}

	var taxonomy *dataImportTaxonomyResolver
	if dataImportNeedsTaxonomy(req) {
		console, consoleErr := h.accountConsoleService()
		if consoleErr != nil {
			return result, consoleErr
		}
		taxonomy, err = newDataImportTaxonomyResolver(ctx, console)
		if err != nil {
			return result, err
		}
	}
	var groups *dataImportGroupResolver
	if dataImportNeedsGroupNames(req.Data.Accounts) {
		allGroups, groupErr := h.adminService.GetAllGroupsIncludingInactive(ctx)
		if groupErr != nil {
			return result, groupErr
		}
		groups = newDataImportGroupResolver(allGroups)
	}

	skipDefaultGroupBind := true
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	for index := range req.Data.Accounts {
		item := req.Data.Accounts[index]
		enrichCredentialsFromIDToken(&item)
		itemResult := DataImportItemResult{Index: index, Name: item.Name}
		if validateErr := validateDataAccountV2(item); validateErr != nil {
			h.recordDataImportFailure(&result, &itemResult, validateErr)
			continue
		}

		keys := dataAccountIdentityKeys(item.Platform, item.Credentials, item.Extra)
		matches := identityIndex.Find(keys)
		if len(matches) > 1 {
			h.recordDataImportFailure(&result, &itemResult, errors.New("multiple current identity matches"))
			continue
		}
		if len(matches) == 1 {
			itemResult.Action = dataImportActionUpdate
			existing, ok := accountsByID[matches[0].AccountID]
			if !ok {
				h.recordDataImportFailure(&result, &itemResult, errors.New("current identity match is unavailable"))
				continue
			}
			updated, warnings, updateErr := h.updateImportedDataAccount(ctx, item, existing, req.UniformSettings, DataImportItemOverrides{}, taxonomy)
			if updateErr != nil {
				h.recordDataImportFailure(&result, &itemResult, updateErr)
				continue
			}
			itemResult.Name = updated.Name
			itemResult.Warnings = warnings
			id := updated.ID
			itemResult.AccountID = &id
			result.AccountUpdated++
			result.AccountIDs = append(result.AccountIDs, id)
			result.Items = append(result.Items, itemResult)
			accountsByID[id] = *updated
			identityIndex.Add(*updated)
			h.scheduleGrokImportProbe(updated)
		} else {
			itemResult.Action = dataImportActionCreate
			created, warnings, createErr := h.createImportedDataAccount(ctx, item, req.UniformSettings, DataImportItemOverrides{}, proxyKeyToID, groups, taxonomy, skipDefaultGroupBind)
			if createErr != nil {
				h.recordDataImportFailure(&result, &itemResult, createErr)
				continue
			}
			itemResult.Name = created.Name
			itemResult.Warnings = warnings
			id := created.ID
			itemResult.AccountID = &id
			result.AccountCreated++
			result.AccountIDs = append(result.AccountIDs, id)
			result.Items = append(result.Items, itemResult)
			accountsByID[id] = *created
			identityIndex.Add(*created)
			h.scheduleGrokImportProbe(created)
		}
	}
	return result, nil
}

func (h *AccountHandler) recordDataImportFailure(result *DataImportResult, item *DataImportItemResult, err error) {
	result.AccountFailed++
	item.Action = "failed"
	item.Error = err.Error()
	result.Items = append(result.Items, *item)
	result.Errors = append(result.Errors, DataImportError{Kind: "account", Name: item.Name, Message: err.Error()})
}

func (h *AccountHandler) createImportedDataAccount(
	ctx context.Context,
	item DataAccount,
	uniform DataImportUniformSettings,
	overrides DataImportItemOverrides,
	proxyKeyToID map[string]int64,
	groups *dataImportGroupResolver,
	taxonomy *dataImportTaxonomyResolver,
	skipDefaultGroupBind bool,
) (*service.Account, []string, error) {
	name := item.Name
	if uniform.NamePrefix != nil {
		name = *uniform.NamePrefix + name
	}
	if uniform.NameSuffix != nil {
		name += *uniform.NameSuffix
	}
	if overrides.Name != nil {
		name = *overrides.Name
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, errors.New("effective account name is empty")
	}

	notes := item.Notes
	if uniform.Notes != nil {
		notes = applyDataImportNotes(notes, *uniform.Notes)
	}
	if overrides.Notes != nil {
		notes = applyDataImportNotes(notes, *overrides.Notes)
	}

	proxyID, err := resolveDataImportCreateProxy(item, uniform, overrides, proxyKeyToID)
	if err != nil {
		return nil, nil, err
	}
	groupIDs, err := resolveDataImportCreateGroups(item, uniform, overrides, groups)
	if err != nil {
		return nil, nil, err
	}
	if err := h.validateDataImportReferences(ctx, groupIDs, proxyID); err != nil {
		return nil, nil, err
	}

	concurrency := item.Concurrency
	if uniform.Concurrency != nil {
		concurrency = *uniform.Concurrency
	}
	if overrides.Concurrency != nil {
		concurrency = *overrides.Concurrency
	}
	priority := item.Priority
	if uniform.Priority != nil {
		priority = *uniform.Priority
	}
	if overrides.Priority != nil {
		priority = *overrides.Priority
	}
	rateMultiplier := item.RateMultiplier
	if uniform.RateMultiplier != nil {
		rateMultiplier = uniform.RateMultiplier
	}
	if overrides.RateMultiplier != nil {
		rateMultiplier = overrides.RateMultiplier
	}

	created, err := h.adminService.CreateAccount(ctx, &service.CreateAccountInput{
		Name: name, Notes: notes, Platform: item.Platform, Type: item.Type,
		Credentials: item.Credentials, Extra: item.Extra, ProxyID: proxyID,
		Concurrency: concurrency, Priority: priority, RateMultiplier: rateMultiplier,
		GroupIDs: groupIDs, ExpiresAt: item.ExpiresAt, AutoPauseOnExpired: item.AutoPauseOnExpired,
		SkipDefaultGroupBind: skipDefaultGroupBind,
	})
	if err != nil {
		return nil, nil, err
	}

	warnings := make([]string, 0)
	status, statusExplicit, statusErr := resolveDataImportCreateStatus(item, uniform, overrides)
	if statusErr != nil {
		return nil, nil, statusErr
	}
	if statusExplicit && status != service.StatusActive {
		if updated, updateErr := h.adminService.UpdateAccount(ctx, created.ID, &service.UpdateAccountInput{Status: status}); updateErr != nil {
			warnings = append(warnings, "account was created but status could not be applied: "+updateErr.Error())
		} else {
			created = updated
		}
	}
	schedulable, schedulableExplicit := resolveDataImportCreateSchedulable(item, uniform, overrides)
	if schedulableExplicit && created.Schedulable != schedulable {
		if updated, updateErr := h.adminService.SetAccountSchedulable(ctx, created.ID, schedulable); updateErr != nil {
			warnings = append(warnings, "account was created but schedulable could not be applied: "+updateErr.Error())
		} else {
			created = updated
		}
	}
	selection := resolveDataImportCreateTaxonomy(item, uniform, overrides)
	if selection.FolderName != nil || selection.TagNames != nil {
		if taxonomy == nil {
			warnings = append(warnings, "account was created but taxonomy service is unavailable")
		} else if updated, taxonomyErr := taxonomy.apply(ctx, created.ID, selection, nil); taxonomyErr != nil {
			warnings = append(warnings, "account was created but taxonomy could not be applied: "+taxonomyErr.Error())
		} else {
			created = updated
		}
	}
	return created, warnings, nil
}

func (h *AccountHandler) updateImportedDataAccount(
	ctx context.Context,
	item DataAccount,
	existing service.Account,
	uniform DataImportUniformSettings,
	overrides DataImportItemOverrides,
	taxonomy *dataImportTaxonomyResolver,
) (*service.Account, []string, error) {
	input := &service.UpdateAccountInput{Type: item.Type, Credentials: item.Credentials, ExpiresAt: item.ExpiresAt}
	if len(item.Extra) > 0 {
		input.Extra = mergeDataImportMaps(existing.Extra, item.Extra)
	}
	if overrides.Name != nil {
		input.Name = strings.TrimSpace(*overrides.Name)
	} else if uniform.NamePrefix != nil || uniform.NameSuffix != nil {
		input.Name = item.Name
		if uniform.NamePrefix != nil {
			input.Name = *uniform.NamePrefix + input.Name
		}
		if uniform.NameSuffix != nil {
			input.Name += *uniform.NameSuffix
		}
		input.Name = strings.TrimSpace(input.Name)
	}
	if input.Name == "" && (overrides.Name != nil || uniform.NamePrefix != nil || uniform.NameSuffix != nil) {
		return nil, nil, errors.New("effective account name is empty")
	}

	if uniform.Notes != nil {
		input.Notes = applyDataImportNotes(existing.Notes, *uniform.Notes)
	}
	if overrides.Notes != nil {
		base := existing.Notes
		if input.Notes != nil {
			base = input.Notes
		}
		input.Notes = applyDataImportNotes(base, *overrides.Notes)
	}
	input.GroupIDs = firstDataImportIDListOverride(uniform.GroupIDs, overrides.GroupIDs)
	input.ProxyID = firstDataImportInt64Override(uniform.ProxyID, overrides.ProxyID)
	input.Concurrency = firstDataImportIntOverride(uniform.Concurrency, overrides.Concurrency)
	input.Priority = firstDataImportIntOverride(uniform.Priority, overrides.Priority)
	input.RateMultiplier = firstDataImportFloatOverride(uniform.RateMultiplier, overrides.RateMultiplier)
	status := firstDataImportStringOverride(uniform.Status, overrides.Status)
	if status != nil {
		normalized, err := normalizeDataAccountStatus(*status)
		if err != nil {
			return nil, nil, err
		}
		input.Status = normalized
	}
	if err := h.validateDataImportReferences(ctx, valueOrNil(input.GroupIDs), input.ProxyID); err != nil {
		return nil, nil, err
	}

	updated, err := h.adminService.UpdateAccount(ctx, existing.ID, input)
	if err != nil {
		return nil, nil, err
	}
	warnings := make([]string, 0)
	if schedulable := firstDataImportBoolOverride(uniform.Schedulable, overrides.Schedulable); schedulable != nil && updated.Schedulable != *schedulable {
		if changed, updateErr := h.adminService.SetAccountSchedulable(ctx, updated.ID, *schedulable); updateErr != nil {
			warnings = append(warnings, "credentials were updated but schedulable could not be applied: "+updateErr.Error())
		} else {
			updated = changed
		}
	}
	selection := resolveDataImportUpdateTaxonomy(uniform, overrides)
	if selection.FolderName != nil || selection.TagNames != nil {
		if taxonomy == nil {
			warnings = append(warnings, "credentials were updated but taxonomy service is unavailable")
		} else if changed, taxonomyErr := taxonomy.apply(ctx, updated.ID, selection, updated); taxonomyErr != nil {
			warnings = append(warnings, "credentials were updated but taxonomy could not be applied: "+taxonomyErr.Error())
		} else {
			updated = changed
		}
	}
	return updated, warnings, nil
}

func (h *AccountHandler) validateDataImportReferences(ctx context.Context, groupIDs []int64, proxyID *int64) error {
	if cache, ok := dataImportReferenceCacheFromContext(ctx); ok {
		for _, groupID := range groupIDs {
			if _, exists := cache.groupIDs[groupID]; !exists {
				return fmt.Errorf("group %d is unavailable", groupID)
			}
		}
		if proxyID != nil && *proxyID > 0 {
			if _, exists := cache.proxyIDs[*proxyID]; !exists {
				return fmt.Errorf("proxy %d is unavailable", *proxyID)
			}
		}
		return nil
	}
	for _, groupID := range groupIDs {
		if _, err := h.adminService.GetGroup(ctx, groupID); err != nil {
			return fmt.Errorf("group %d is unavailable: %w", groupID, err)
		}
	}
	if proxyID != nil && *proxyID > 0 {
		if _, err := h.adminService.GetProxy(ctx, *proxyID); err != nil {
			return fmt.Errorf("proxy %d is unavailable: %w", *proxyID, err)
		}
	}
	return nil
}

func resolveDataImportCreateProxy(item DataAccount, uniform DataImportUniformSettings, overrides DataImportItemOverrides, proxyKeyToID map[string]int64) (*int64, error) {
	var proxyID *int64
	if item.ProxyKey != nil && strings.TrimSpace(*item.ProxyKey) != "" {
		id, ok := proxyKeyToID[*item.ProxyKey]
		if !ok {
			return nil, errors.New("proxy_key not found")
		}
		proxyID = &id
	}
	if uniform.ProxyID != nil {
		value := *uniform.ProxyID
		proxyID = &value
	}
	if overrides.ProxyID != nil {
		value := *overrides.ProxyID
		proxyID = &value
	}
	// UpdateAccount uses zero to clear an existing proxy, while CreateAccount
	// expects nil for a direct account because proxy_id is a foreign key.
	if proxyID != nil && *proxyID == 0 {
		return nil, nil
	}
	return proxyID, nil
}

func resolveDataImportCreateGroups(item DataAccount, uniform DataImportUniformSettings, overrides DataImportItemOverrides, resolver *dataImportGroupResolver) ([]int64, error) {
	var groupIDs []int64
	if len(item.Groups) > 0 {
		if resolver == nil {
			return nil, errors.New("group resolver is unavailable")
		}
		resolved, err := resolver.resolve(item.Platform, item.Groups)
		if err != nil {
			return nil, err
		}
		groupIDs = resolved
	}
	if uniform.GroupIDs != nil {
		groupIDs = append([]int64(nil), (*uniform.GroupIDs)...)
	}
	if overrides.GroupIDs != nil {
		groupIDs = append([]int64(nil), (*overrides.GroupIDs)...)
	}
	return uniqueDataImportIDs(groupIDs), nil
}

func resolveDataImportCreateStatus(item DataAccount, uniform DataImportUniformSettings, overrides DataImportItemOverrides) (string, bool, error) {
	status := item.Status
	explicit := status != ""
	if uniform.Status != nil {
		status, explicit = *uniform.Status, true
	}
	if overrides.Status != nil {
		status, explicit = *overrides.Status, true
	}
	if !explicit {
		return service.StatusActive, false, nil
	}
	normalized, err := normalizeDataAccountStatus(status)
	return normalized, true, err
}

func resolveDataImportCreateSchedulable(item DataAccount, uniform DataImportUniformSettings, overrides DataImportItemOverrides) (bool, bool) {
	value, explicit := true, false
	if item.Schedulable != nil {
		value, explicit = *item.Schedulable, true
	}
	if uniform.Schedulable != nil {
		value, explicit = *uniform.Schedulable, true
	}
	if overrides.Schedulable != nil {
		value, explicit = *overrides.Schedulable, true
	}
	return value, explicit
}

func resolveDataImportCreateTaxonomy(item DataAccount, uniform DataImportUniformSettings, overrides DataImportItemOverrides) dataImportTaxonomySelection {
	selection := dataImportTaxonomySelection{FolderName: item.ManagementFolder}
	if item.Tags != nil {
		tags := append([]string(nil), item.Tags...)
		selection.TagNames = &tags
	}
	if uniform.ManagementFolder != nil {
		selection.FolderName = uniform.ManagementFolder
	}
	if uniform.Tags != nil {
		selection.TagNames = uniform.Tags
	}
	if overrides.ManagementFolder != nil {
		selection.FolderName = overrides.ManagementFolder
	}
	if overrides.Tags != nil {
		selection.TagNames = overrides.Tags
	}
	return selection
}

func resolveDataImportUpdateTaxonomy(uniform DataImportUniformSettings, overrides DataImportItemOverrides) dataImportTaxonomySelection {
	selection := dataImportTaxonomySelection{FolderName: uniform.ManagementFolder, TagNames: uniform.Tags}
	if overrides.ManagementFolder != nil {
		selection.FolderName = overrides.ManagementFolder
	}
	if overrides.Tags != nil {
		selection.TagNames = overrides.Tags
	}
	return selection
}

func applyDataImportNotes(current *string, setting DataImportNotesSetting) *string {
	value := setting.Value
	if setting.Mode == "replace" {
		return &value
	}
	base := ""
	if current != nil {
		base = *current
	}
	if base == "" {
		return &value
	}
	if value == "" {
		out := base
		return &out
	}
	out := base + "\n" + value
	return &out
}

func mergeDataImportMaps(existing, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(incoming))
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range incoming {
		out[key] = value
	}
	return out
}

func firstDataImportIDListOverride(uniform, item *[]int64) *[]int64 {
	if item != nil {
		value := append([]int64(nil), (*item)...)
		return &value
	}
	if uniform != nil {
		value := append([]int64(nil), (*uniform)...)
		return &value
	}
	return nil
}

func firstDataImportInt64Override(uniform, item *int64) *int64 {
	if item != nil {
		value := *item
		return &value
	}
	if uniform != nil {
		value := *uniform
		return &value
	}
	return nil
}

func firstDataImportIntOverride(uniform, item *int) *int {
	if item != nil {
		value := *item
		return &value
	}
	if uniform != nil {
		value := *uniform
		return &value
	}
	return nil
}

func firstDataImportFloatOverride(uniform, item *float64) *float64 {
	if item != nil {
		value := *item
		return &value
	}
	if uniform != nil {
		value := *uniform
		return &value
	}
	return nil
}

func firstDataImportStringOverride(uniform, item *string) *string {
	if item != nil {
		value := *item
		return &value
	}
	if uniform != nil {
		value := *uniform
		return &value
	}
	return nil
}

func firstDataImportBoolOverride(uniform, item *bool) *bool {
	if item != nil {
		value := *item
		return &value
	}
	if uniform != nil {
		value := *uniform
		return &value
	}
	return nil
}

func valueOrNil(value *[]int64) []int64 {
	if value == nil {
		return nil
	}
	return *value
}

func uniqueDataImportIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func dataImportNeedsTaxonomy(req DataImportRequest) bool {
	if req.UniformSettings.ManagementFolder != nil || req.UniformSettings.Tags != nil {
		return true
	}
	for _, item := range req.Data.Accounts {
		if item.ManagementFolder != nil || item.Tags != nil {
			return true
		}
	}
	return false
}

func dataImportNeedsGroupNames(accounts []DataAccount) bool {
	for _, account := range accounts {
		if len(account.Groups) > 0 {
			return true
		}
	}
	return false
}

func newDataImportGroupResolver(groups []service.Group) *dataImportGroupResolver {
	resolver := &dataImportGroupResolver{byName: make(map[string][]service.Group)}
	for _, group := range groups {
		key := strings.ToLower(strings.TrimSpace(group.Name))
		resolver.byName[key] = append(resolver.byName[key], group)
	}
	return resolver
}

func (resolver *dataImportGroupResolver) resolve(platform string, names []string) ([]int64, error) {
	ids := make([]int64, 0, len(names))
	for _, name := range names {
		candidates := resolver.byName[strings.ToLower(strings.TrimSpace(name))]
		matchingPlatform := make([]service.Group, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.Platform == platform {
				matchingPlatform = append(matchingPlatform, candidate)
			}
		}
		if len(matchingPlatform) == 1 {
			ids = append(ids, matchingPlatform[0].ID)
			continue
		}
		if len(matchingPlatform) > 1 {
			return nil, fmt.Errorf("group %q is ambiguous for platform %s", name, platform)
		}
		if len(candidates) == 1 {
			ids = append(ids, candidates[0].ID)
			continue
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("group %q does not exist", name)
		}
		return nil, fmt.Errorf("group %q is ambiguous", name)
	}
	return uniqueDataImportIDs(ids), nil
}

func newDataImportTaxonomyResolver(ctx context.Context, console accountConsoleAdminService) (*dataImportTaxonomyResolver, error) {
	folders, err := console.ListAccountFolders(ctx)
	if err != nil {
		return nil, err
	}
	tags, err := console.ListAccountTags(ctx)
	if err != nil {
		return nil, err
	}
	resolver := &dataImportTaxonomyResolver{
		service: console, folders: make(map[string]service.AccountManagementFolder, len(folders)),
		tags: make(map[string]service.AccountManagementTag, len(tags)),
	}
	for _, folder := range folders {
		resolver.folders[strings.ToLower(strings.TrimSpace(folder.Name))] = folder
	}
	for _, tag := range tags {
		resolver.tags[strings.ToLower(strings.TrimSpace(tag.Name))] = tag
	}
	return resolver, nil
}

func (resolver *dataImportTaxonomyResolver) resolveFolder(ctx context.Context, name string) (*int64, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, false, nil
	}
	key := strings.ToLower(name)
	if folder, ok := resolver.folders[key]; ok {
		id := folder.ID
		return &id, false, nil
	}
	created, err := resolver.service.CreateAccountFolder(ctx, service.AccountTaxonomyInput{Name: name})
	if err != nil {
		folders, listErr := resolver.service.ListAccountFolders(ctx)
		if listErr == nil {
			for _, folder := range folders {
				resolver.folders[strings.ToLower(strings.TrimSpace(folder.Name))] = folder
			}
			if folder, ok := resolver.folders[key]; ok {
				id := folder.ID
				return &id, false, nil
			}
		}
		return nil, false, err
	}
	resolver.folders[key] = *created
	id := created.ID
	return &id, true, nil
}

func (resolver *dataImportTaxonomyResolver) resolveTags(ctx context.Context, names []string) ([]int64, []int64, error) {
	ids := make([]int64, 0, len(names))
	createdIDs := make([]int64, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, rawName := range names {
		name := strings.TrimSpace(rawName)
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if tag, ok := resolver.tags[key]; ok {
			ids = append(ids, tag.ID)
			continue
		}
		created, err := resolver.service.CreateAccountTag(ctx, service.AccountTaxonomyInput{Name: name})
		if err != nil {
			tags, listErr := resolver.service.ListAccountTags(ctx)
			if listErr == nil {
				for _, tag := range tags {
					resolver.tags[strings.ToLower(strings.TrimSpace(tag.Name))] = tag
				}
				if tag, ok := resolver.tags[key]; ok {
					ids = append(ids, tag.ID)
					continue
				}
			}
			return nil, createdIDs, err
		}
		resolver.tags[key] = *created
		ids = append(ids, created.ID)
		createdIDs = append(createdIDs, created.ID)
	}
	return ids, createdIDs, nil
}

func (resolver *dataImportTaxonomyResolver) removeFolderFromCache(id int64) {
	for key, folder := range resolver.folders {
		if folder.ID == id {
			delete(resolver.folders, key)
			return
		}
	}
}

func (resolver *dataImportTaxonomyResolver) removeTagFromCache(id int64) {
	for key, tag := range resolver.tags {
		if tag.ID == id {
			delete(resolver.tags, key)
			return
		}
	}
}

func (resolver *dataImportTaxonomyResolver) cleanupUnused(ctx context.Context, folderID *int64, tagIDs []int64) error {
	cleanupErrors := make([]string, 0)
	for index := len(tagIDs) - 1; index >= 0; index-- {
		id := tagIDs[index]
		if err := resolver.service.DeleteAccountTag(ctx, id); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("tag %d: %v", id, err))
			continue
		}
		resolver.removeTagFromCache(id)
	}
	if folderID != nil {
		if err := resolver.service.DeleteAccountFolder(ctx, *folderID, false); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Sprintf("folder %d: %v", *folderID, err))
		} else {
			resolver.removeFolderFromCache(*folderID)
		}
	}
	if len(cleanupErrors) > 0 {
		return errors.New(strings.Join(cleanupErrors, "; "))
	}
	return nil
}

func (resolver *dataImportTaxonomyResolver) apply(ctx context.Context, accountID int64, selection dataImportTaxonomySelection, current *service.Account) (*service.Account, error) {
	var folderID *int64
	var createdFolderID *int64
	var tagIDs []int64
	var createdTagIDs []int64
	var err error
	if selection.FolderName != nil {
		var created bool
		folderID, created, err = resolver.resolveFolder(ctx, *selection.FolderName)
		if err != nil {
			return nil, err
		}
		if created {
			createdFolderID = folderID
		}
	} else if current != nil && current.ManagementFolder != nil {
		id := current.ManagementFolder.ID
		folderID = &id
	}
	if selection.TagNames != nil {
		tagIDs, createdTagIDs, err = resolver.resolveTags(ctx, *selection.TagNames)
		if err != nil {
			if cleanupErr := resolver.cleanupUnused(ctx, createdFolderID, createdTagIDs); cleanupErr != nil {
				return nil, fmt.Errorf("%w; taxonomy cleanup failed: %v", err, cleanupErr)
			}
			return nil, err
		}
	} else if current != nil {
		for _, tag := range current.Tags {
			tagIDs = append(tagIDs, tag.ID)
		}
	}
	updated, err := resolver.service.SetAccountTaxonomy(ctx, accountID, service.AccountTaxonomyAssignment{FolderID: folderID, TagIDs: tagIDs})
	if err == nil {
		return updated, nil
	}
	if cleanupErr := resolver.cleanupUnused(ctx, createdFolderID, createdTagIDs); cleanupErr != nil {
		return nil, fmt.Errorf("%w; taxonomy cleanup failed: %v", err, cleanupErr)
	}
	return nil, err
}

func (h *AccountHandler) importDataProxies(ctx context.Context, items []DataProxy, result *DataImportResult) (map[string]int64, error) {
	existingProxies, err := h.listAllProxies(ctx)
	if err != nil {
		return nil, err
	}
	proxyKeyToID := make(map[string]int64, len(existingProxies)+len(items))
	proxyNameToID := make(map[string]int64, len(existingProxies)+len(items))
	for _, proxy := range existingProxies {
		proxyKeyToID[buildProxyKey(proxy.Protocol, proxy.Host, proxy.Port, proxy.Username, proxy.Password)] = proxy.ID
		if proxy.Name != "" {
			proxyNameToID[proxy.Name] = proxy.ID
		}
	}
	for _, item := range items {
		key := item.ProxyKey
		if key == "" {
			key = buildProxyKey(item.Protocol, item.Host, item.Port, item.Username, item.Password)
		}
		if validateErr := validateDataProxy(item); validateErr != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, DataImportError{Kind: "proxy", Name: item.Name, ProxyKey: key, Message: validateErr.Error()})
			continue
		}
		normalizedStatus := normalizeProxyStatus(item.Status)
		if existingID, ok := proxyKeyToID[key]; ok {
			result.ProxyReused++
			if normalizedStatus != "" {
				if proxy, getErr := h.adminService.GetProxy(ctx, existingID); getErr == nil && proxy != nil && proxy.Status != normalizedStatus {
					expiresAt, fallbackMode, backupProxyID := dataImportProxySettings(item, proxyNameToID)
					_, _ = h.adminService.UpdateProxy(ctx, existingID, &service.UpdateProxyInput{
						Status: normalizedStatus, ExpiresAt: expiresAt, FallbackMode: fallbackMode,
						BackupProxyID: backupProxyID, ExpiryWarnDays: item.ExpiryWarnDays,
						Name: proxy.Name, Protocol: proxy.Protocol, Host: proxy.Host, Port: proxy.Port,
						Username: proxy.Username, Password: proxy.Password,
					})
				}
			}
			continue
		}
		expiresAt, fallbackMode, backupProxyID := dataImportProxySettings(item, proxyNameToID)
		if item.BackupProxyName != "" && backupProxyID == nil {
			result.Errors = append(result.Errors, DataImportError{Kind: "proxy", Name: item.Name, ProxyKey: key, Message: fmt.Sprintf("backup_proxy_name %q not found, fallback_mode downgraded to none", item.BackupProxyName)})
		}
		created, createErr := h.adminService.CreateProxy(ctx, &service.CreateProxyInput{
			Name: defaultProxyName(item.Name), Protocol: item.Protocol, Host: item.Host, Port: item.Port,
			Username: item.Username, Password: item.Password, ExpiresAt: expiresAt,
			FallbackMode: fallbackMode, BackupProxyID: backupProxyID, ExpiryWarnDays: item.ExpiryWarnDays,
		})
		if createErr != nil {
			result.ProxyFailed++
			result.Errors = append(result.Errors, DataImportError{Kind: "proxy", Name: item.Name, ProxyKey: key, Message: createErr.Error()})
			continue
		}
		proxyKeyToID[key] = created.ID
		if created.Name != "" {
			proxyNameToID[created.Name] = created.ID
		}
		result.ProxyCreated++
		if normalizedStatus != "" && normalizedStatus != created.Status {
			_, _ = h.adminService.UpdateProxy(ctx, created.ID, &service.UpdateProxyInput{
				Status: normalizedStatus, ExpiresAt: expiresAt, FallbackMode: fallbackMode,
				BackupProxyID: backupProxyID, ExpiryWarnDays: item.ExpiryWarnDays,
				Name: created.Name, Protocol: created.Protocol, Host: created.Host, Port: created.Port,
				Username: created.Username, Password: created.Password,
			})
		}
	}
	return proxyKeyToID, nil
}

func dataImportProxySettings(item DataProxy, names map[string]int64) (*time.Time, string, *int64) {
	var expiresAt *time.Time
	if item.ExpiresAt != nil {
		value := time.Unix(*item.ExpiresAt, 0).UTC()
		expiresAt = &value
	}
	fallbackMode := item.FallbackMode
	var backupProxyID *int64
	if item.BackupProxyName != "" {
		if id, ok := names[item.BackupProxyName]; ok {
			backupProxyID = &id
		} else {
			fallbackMode = service.FallbackModeNone
		}
	}
	return expiresAt, fallbackMode, backupProxyID
}
