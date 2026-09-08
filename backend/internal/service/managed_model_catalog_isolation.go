package service

// FilterManagedModelSelectors removes only the reserved routing namespace from
// client-visible model IDs. Account mappings remain complete for internal
// routing, including when an account is shared with an unmanaged private group.
// Retained IDs keep their original spelling and order; input is never mutated.
func FilterManagedModelSelectors(modelIDs []string) []string {
	first := -1
	for i, model := range modelIDs {
		if IsManagedModelSelector(model) {
			first = i
			break
		}
	}
	if first < 0 {
		return modelIDs
	}
	filtered := make([]string, 0, len(modelIDs)-1)
	filtered = append(filtered, modelIDs[:first]...)
	for _, model := range modelIDs[first+1:] {
		if !IsManagedModelSelector(model) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}
