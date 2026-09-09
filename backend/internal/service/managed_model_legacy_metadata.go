package service

// projectManagedModelLegacyMetadata pins metadata to the retained v1 path.
// Generative branches never acquire count_tokens or WebSocket capabilities
// from their basic liveness evidence. The route graph stays intact for current
// compilation/account validation and only its request-local legacy fields are
// projected for existing metadata handlers.
func projectManagedModelLegacyMetadata(request *ManagedModelRequest) error {
	if request == nil || request.Version != ManagedModelRoutesVersion || len(request.Route.Branches) == 0 || !managedModelLegacyMetadataEndpoint(request.Endpoint) {
		return nil
	}
	var retained *ManagedModelRouteBranch
	for _, branch := range request.Route.Branches {
		if branch.UpstreamProtocol != "" || !managedModelHasEndpoint(branch.Endpoints, request.Endpoint) {
			continue
		}
		if retained != nil {
			return ErrManagedModelRouteUnavailable
		}
		copy := branch
		retained = &copy
	}
	if retained == nil {
		return ErrManagedModelRouteUnavailable
	}
	request.Branch = retained
	request.Route.Selector = retained.Selector
	request.Route.TargetPlatform = retained.TargetPlatform
	request.Route.Accounts = retained.Accounts
	return nil
}

func managedModelLegacyMetadataEndpoint(endpoint string) bool {
	return endpoint == CompositeRouteEndpointCountTokens || endpoint == ManagedModelEndpointResponsesWebSocket
}

// IsManagedModelLegacyMetadataRequest identifies a resolved v2 request that is
// explicitly pinned to its retained metadata path, not a generative attempt.
func IsManagedModelLegacyMetadataRequest(request *ManagedModelRequest) bool {
	if request == nil || request.Version != ManagedModelRoutesVersion || !managedModelLegacyMetadataEndpoint(request.Endpoint) ||
		request.Branch == nil || request.Branch.UpstreamProtocol != "" || len(request.Route.Branches) == 0 ||
		request.Branch.Selector != ManagedModelSelector(request.GroupID, request.Route.PublicModel) ||
		request.Route.Selector != request.Branch.Selector || request.Route.TargetPlatform != request.Branch.TargetPlatform ||
		!managedModelHasEndpoint(request.Route.Endpoints, request.Endpoint) || !managedModelHasEndpoint(request.Branch.Endpoints, request.Endpoint) {
		return false
	}
	retained := 0
	for _, branch := range request.Route.Branches {
		if branch.UpstreamProtocol == "" && managedModelHasEndpoint(branch.Endpoints, request.Endpoint) {
			if branch.Selector != request.Branch.Selector || branch.TargetPlatform != request.Branch.TargetPlatform {
				return false
			}
			retained++
		}
	}
	return retained == 1
}
