package service

// P3f-c 资产域服务（对齐 legacy app/api/routes/assets.py + projects.py scene-options 路由）：
// 统一把 repository 的 KeyError/PermissionError/ValueError 映射为 StatusError，
// 错误文案逐字对齐 legacy HTTPException detail。

import (
	"context"

	"cineforge/server/internal/repository"
)

// AssetService 资产域服务。
type AssetService struct {
	assets *repository.Assets
}

// NewAssetService 创建资产域服务。
func NewAssetService(assets *repository.Assets) *AssetService {
	return &AssetService{assets: assets}
}

// assetStatusError 统一映射：KeyError→404、PermissionError→403、ValueError→400；
// 其余（数据库/IO 错误）原样返回，路由按 500 处理。
func assetStatusError(notFoundDetail string, forbiddenDetail string, err error) error {
	switch {
	case repository.IsNotFoundError(err):
		return notFound404(notFoundDetail)
	case repository.IsForbiddenError(err):
		return forbidden403(forbiddenDetail)
	default:
		if detail, ok := repository.BadRequestDetail(err); ok {
			return badRequest400(detail)
		}
		return err
	}
}

// CreateAsset 对齐 POST /assets。
func (s *AssetService) CreateAsset(ctx context.Context, actor repository.TaskActor, input repository.CreateAssetInput) (*repository.AssetView, error) {
	out, err := s.assets.CreateAsset(ctx, actor, input)
	if err != nil {
		return nil, assetStatusError("Project not found", "Asset permission denied", err)
	}
	return out, nil
}

// CreateTemporaryAssetProduction 对齐 POST /assets/temporary-production。
func (s *AssetService) CreateTemporaryAssetProduction(ctx context.Context, actor repository.TaskActor, input repository.TemporaryProductionInput) (*repository.TemporaryProductionResult, error) {
	out, err := s.assets.CreateTemporaryAssetProduction(ctx, actor, input)
	if err != nil {
		return nil, assetStatusError("Project or episode not found", "Asset permission denied", err)
	}
	return out, nil
}

// ListAssetLibrary 对齐 GET /assets/library。
func (s *AssetService) ListAssetLibrary(ctx context.Context, actor repository.TaskActor, projectID *string, includeVersions bool) ([]repository.AssetLibraryItemView, error) {
	out, err := s.assets.ListAssetLibrary(ctx, actor, projectID, includeVersions)
	if err != nil {
		return nil, assetStatusError("Project not found", "Asset library permission denied", err)
	}
	return out, nil
}

// ListAssets 对齐 GET /assets。
func (s *AssetService) ListAssets(ctx context.Context, actor repository.TaskActor, params repository.ListAssetsParams) ([]repository.AssetView, error) {
	out, err := s.assets.ListAssets(ctx, actor, params)
	if err != nil {
		return nil, assetStatusError("Asset not found", "Asset permission denied", err)
	}
	return out, nil
}

// GetAsset 对齐 GET /assets/{id}。
func (s *AssetService) GetAsset(ctx context.Context, actor repository.TaskActor, assetID string) (*repository.AssetView, error) {
	out, err := s.assets.GetAsset(ctx, actor, assetID)
	if err != nil {
		return nil, assetStatusError("Asset not found", "Asset permission denied", err)
	}
	return out, nil
}

// ListAssetVariantPlans 对齐 GET /assets/variant-plans。
func (s *AssetService) ListAssetVariantPlans(ctx context.Context, actor repository.TaskActor, projectID string, episodeID *string) ([]repository.AssetVariantPlanView, error) {
	out, err := s.assets.ListAssetVariantPlans(ctx, actor, projectID, episodeID)
	if err != nil {
		return nil, assetStatusError("Project not found", "Asset variant permission denied", err)
	}
	return out, nil
}

// CreateAssetVariantPlan 对齐 POST /assets/variant-plans（恒 400 逐字）。
func (s *AssetService) CreateAssetVariantPlan(ctx context.Context) (*repository.AssetVariantPlanView, error) {
	out, err := s.assets.CreateAssetVariantPlan(ctx)
	if err != nil {
		return nil, assetStatusError("Project or asset not found", "Asset variant permission denied", err)
	}
	return out, nil
}

// UpdateAssetVariantPlan 对齐 PATCH /assets/variant-plans/{id}（DELETE 复用 status=retired）。
func (s *AssetService) UpdateAssetVariantPlan(ctx context.Context, actor repository.TaskActor, planID string, input repository.UpdateAssetVariantPlanInput) (*repository.AssetVariantPlanView, error) {
	out, err := s.assets.UpdateAssetVariantPlan(ctx, actor, planID, input)
	if err != nil {
		return nil, assetStatusError("Variant plan not found", "Asset variant permission denied", err)
	}
	return out, nil
}

// ListSceneAssetOptions 对齐 GET /projects/{pid}/asset-revisions/current/scene-options。
func (s *AssetService) ListSceneAssetOptions(ctx context.Context, actor repository.TaskActor, projectID, search string) ([]repository.SceneAssetOptionView, error) {
	out, err := s.assets.ListSceneAssetOptions(ctx, actor, projectID, search)
	if err != nil {
		return nil, assetStatusError("Project not found", "Project permission denied", err)
	}
	return out, nil
}

// EpisodeAssetBindings 对齐分集详情 Assets（project + 当前版本 + active 绑定）。
func (s *AssetService) EpisodeAssetBindings(ctx context.Context, projectID, episodeID, scriptVersionID string) ([]any, error) {
	views, err := s.assets.ListEpisodeAssetBindingViews(ctx, projectID, episodeID, scriptVersionID)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(views))
	for _, v := range views {
		out = append(out, v)
	}
	return out, nil
}
