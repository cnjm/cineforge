package repository

// P3f-b 文件域仓储：files 行读取、任务上传上下文、任务版本计数与公司资产库回退。
// 对齐 legacy repositories.py 的 is_company_asset_library_file、storage.py 的 _next_file_version。

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
)

// Files 基于共享连接池的文件域仓储。
type Files struct {
	pool *pgxpool.Pool
}

// NewFiles 创建 Files 仓储。
func NewFiles(pool *pgxpool.Pool) *Files { return &Files{pool: pool} }

const fileColumns = `id, file_code, bucket, object_key, file_name, mime_type, file_size,
	checksum, uploaded_by, project_id, episode_id, task_id, created_at, updated_at`

func scanFile(row pgx.Row) (*model.Files, error) {
	var f model.Files
	err := row.Scan(&f.ID, &f.FileCode, &f.Bucket, &f.ObjectKey, &f.FileName, &f.MimeType,
		&f.FileSize, &f.Checksum, &f.UploadedBy, &f.ProjectId, &f.EpisodeId, &f.TaskId,
		&f.CreatedAt, &f.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan file: %w", err)
	}
	return &f, nil
}

// FindFileByID 按 id 读取 files 行（对齐 session.get(FileObject)）；不存在返回 (nil, nil)。
func (r *Files) FindFileByID(ctx context.Context, id string) (*model.Files, error) {
	return scanFile(r.pool.QueryRow(ctx, `SELECT `+fileColumns+` FROM files WHERE id = $1`, id))
}

// ---- 任务上传上下文（对齐 storage.upload_task_file 的 project/storyboard/asset 关联） ----

// TaskProjectParts 任务上传对象键所需的 project/storyboard/asset 关联字段。
type TaskProjectParts struct {
	ProjectPrefix *string
	ProjectNo     *string
	ProjectTitle  string
	EpisodeNum    int32
	OrderNum      int32
	AssetCode     *string
}

// TaskProjectParts 对齐 session.get(Project/Storyboard/Asset)；project 缺失 → KeyError 语义
//（路由层映射 404 "Project not found"）。
func (r *Files) TaskProjectParts(ctx context.Context, projectID string, storyboardID, assetID *string) (*TaskProjectParts, error) {
	var p TaskProjectParts
	err := r.pool.QueryRow(ctx,
		`SELECT pr.project_prefix, pr.project_no, pr.title,
		        COALESCE(sb.episode_num, 0), COALESCE(sb.order_num, 0),
		        a.asset_code
		 FROM projects pr
		 LEFT JOIN storyboards sb ON sb.id = $2
		 LEFT JOIN assets a ON a.id = $3
		 WHERE pr.id = $1`, projectID, storyboardID, assetID).
		Scan(&p.ProjectPrefix, &p.ProjectNo, &p.ProjectTitle, &p.EpisodeNum, &p.OrderNum, &p.AssetCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFoundError(projectID)
	}
	if err != nil {
		return nil, fmt.Errorf("load task project parts: %w", err)
	}
	return &p, nil
}

// NextTaskFileVersion 对齐 storage._next_file_version：该任务已上传对象数 + 1
//（object_key 均含 `/TASK-{id8}/V` 段，V{n} 为上传序）。
func (r *Files) NextTaskFileVersion(ctx context.Context, taskID string) (int32, error) {
	var count int32
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM files WHERE object_key LIKE '%' || '/TASK-' || $1 || '/V' || '%'`,
		taskIDPrefix8(taskID)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count task file versions: %w", err)
	}
	return count + 1, nil
}

func taskIDPrefix8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// IsCompanyAssetLibraryFile 对齐 repositories.is_company_asset_library_file：
// 文件须挂到已批准 main master 提交（primary_master/alternate_master、is_selected、
// !is_archived、!is_invalidated），任务未退役、项目未删除，且任务挂正式资产或分镜。
func (r *Files) IsCompanyAssetLibraryFile(ctx context.Context, fileID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM submissions sub
		   JOIN tasks t ON t.id = sub.task_id
		   JOIN projects p ON p.id = t.project_id
		   LEFT JOIN assets a ON a.id = t.asset_id
		   WHERE sub.file_id = $1
		     AND t.is_retired = false
		     AND sub.status IN ('primary_master','alternate_master')
		     AND sub.is_selected = true
		     AND sub.is_archived = false
		     AND sub.is_invalidated = false
		     AND p.deleted_at IS NULL
		     AND ((t.asset_id IS NOT NULL AND a.status NOT IN ('deleted','excluded'))
		       OR (t.asset_id IS NULL AND t.storyboard_id IS NOT NULL)))`, fileID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("company asset library file check: %w", err)
	}
	return exists, nil
}