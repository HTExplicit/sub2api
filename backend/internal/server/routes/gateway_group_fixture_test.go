package routes

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Gateway route fixtures inject an authenticated key, but still need the same
// read-only current-group source as production. Keep the group public and its
// actual allowlist/platform intact so admission and platform-gate assertions
// exercise the full middleware chain rather than bypassing the managed guard.
type gatewayRoutesGroupRepository struct {
	service.GroupRepository
	group *service.Group
}

func (r *gatewayRoutesGroupRepository) GetByIDLite(_ context.Context, id int64) (*service.Group, error) {
	if r.group == nil || r.group.ID != id {
		return nil, service.ErrGroupNotFound
	}
	group := *r.group
	return &group, nil
}

func (r *gatewayRoutesGroupRepository) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	return r.GetByIDLite(ctx, id)
}

func newGatewayRoutesHandlerForGroup(cfg *config.Config, group *service.Group) *handler.GatewayHandler {
	gateway := service.NewGatewayService(nil, &gatewayRoutesGroupRepository{group: group}, nil, nil, nil, nil, nil, nil, cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	return handler.NewGatewayHandler(gateway, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil)
}
