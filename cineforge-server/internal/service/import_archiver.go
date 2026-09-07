package service

// 剧本导入归档器（P3f：真实 MinIO）。
// 对齐 legacy app/services/storage.py 的 upload_project_import_file 与
// app/api/routes/projects.py 的 _archive_import_file / _try_archive_import_file：
//   - 扩展名白名单 .docx/.txt/.md（ValueError → 400，路由层已先行校验，此处防御）
//   - 对象键 `{prefix}/script/imports/{yyyyMMddHHmmss}-{safe_filename}`
//   - file_code `{prefix}-SCRIPT-{uuid8大写}`（8 位大写 hex）
//   - files 登记：uploaded_by/project_id，episode_id/task_id 为 NULL
//   - MinIO 异常 → 由调用方映射 503 "剧本原文件归档失败，未创建正式剧本版本，请稍后重试。"

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"
	"time"

	"cineforge/server/internal/auth"
	"cineforge/server/internal/config"
	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/storage"
)

// ErrUnsupportedImportExt 对齐 legacy storage._extension 白名单的 ValueError 文案。
// 路由层 ValidateScriptUpload 已先行拦截，此处仅保留防御语义。
var ErrUnsupportedImportExt = errors.New("项目导入只支持 .docx、.txt、.md 文件")

// importMimeMap 对齐 Python mimetypes.guess_type 对三类剧本文本的常见解析结果；
// 未知扩展名回退 mime.TypeByExtension → application/octet-stream。
var importMimeMap = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".txt":  "text/plain",
	".md":   "text/markdown",
}

type minioArchiver struct {
	projects *repository.Projects
	client   *storage.Client
	bucket   string
}

// NewMinioArchiver 构造真实 MinIO 归档器；MinIO 凭证未配置时返回错误，
// 调用方（NewRouter）回退 archiveUnavailableArchiver（import 保持 503 契约）。
func NewMinioArchiver(projects *repository.Projects, cfg config.MinIOConfig) (ImportArchiver, error) {
	client, err := storage.New(cfg)
	if err != nil {
		return nil, err
	}
	return &minioArchiver{projects: projects, client: client, bucket: cfg.Bucket}, nil
}

func (a *minioArchiver) ArchiveImportFile(ctx context.Context, projectID string, raw []byte, filename, uploadedBy string) (*model.Files, error) {
	p, err := a.projects.FindByID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, notFound404("Project not found")
	}
	if !isImportExtension(filename) {
		return nil, ErrUnsupportedImportExt
	}
	contentType := guessImportContentType(filename)
	objectKey := projectImportObjectKey(p, filename)
	if err := a.client.Put(ctx, a.bucket, objectKey, bytes.NewReader(raw), int64(len(raw)), contentType); err != nil {
		return nil, fmt.Errorf("archive import file %q: %w", filename, err)
	}
	fileCode := projectImportFileCode(p)
	f := &model.Files{
		ID:         mustUUID(),
		FileCode:   &fileCode,
		Bucket:     a.bucket,
		ObjectKey:  objectKey,
		FileName:   safeFilename(filename),
		MimeType:   &contentType,
		FileSize:   i32Ptr(int32(len(raw))),
		UploadedBy: &uploadedBy,
		ProjectId:  &projectID,
	}
	if err := a.projects.AddFile(ctx, f); err != nil {
		return nil, fmt.Errorf("register import file: %w", err)
	}
	return f, nil
}

// ---- 键/名构造（逐字对齐 legacy storage.py）----

// isImportExtension 对齐 legacy `extension not in {"docx","txt","md"}`。
func isImportExtension(filename string) bool {
	ext := strings.ToLower(extensionOf(filename))
	return ext == ".docx" || ext == ".txt" || ext == ".md"
}

// guessImportContentType 对齐 `content_type or mimetypes.guess_type(...)[0] or "application/octet-stream"`。
func guessImportContentType(filename string) string {
	if ct, ok := importMimeMap[strings.ToLower(extensionOf(filename))]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(extensionOf(filename)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// projectImportObjectKey 对齐 _project_import_object_key（前缀取 prefix|no|title）。
func projectImportObjectKey(p *model.Projects, originalFilename string) string {
	prefix := truncate16(slugUpper(firstNonEmptyStr(ptrOr(p.ProjectPrefix, ""), ptrOr(p.ProjectNo, ""), p.Title), "RF"))
	ts := time.Now().UTC().Format("20060102150405")
	return prefix + "/script/imports/" + ts + "-" + safeFilename(originalFilename)
}

// projectImportFileCode 对齐 `{slug(prefix|no|'RF')[:16]}-SCRIPT-{uuid4.hex[:8].upper()}`。
func projectImportFileCode(p *model.Projects) string {
	prefix := truncate16(slugUpper(firstNonEmptyStr(ptrOr(p.ProjectPrefix, ""), ptrOr(p.ProjectNo, ""), "RF"), "RF"))
	return fmt.Sprintf("%s-SCRIPT-%s", prefix, strings.ToUpper(mustUUID()[:8]))
}

// extensionOf 对齐 legacy _extension：无点号时退化为 "bin"。
func extensionOf(filename string) string {
	if filename == "" {
		return ".bin"
	}
	base := filename
	if idx := strings.LastIndexAny(base, "/\\"); idx >= 0 {
		base = base[idx+1:]
	}
	idx := strings.LastIndexByte(base, '.')
	if idx < 0 || idx == len(base)-1 {
		return ".bin"
	}
	return strings.ToLower(base[idx:])
}

// safeFilename 对齐 legacy _safe_filename：只保留字母数字 `._-` 与中文，其余折叠为 `_`，
// 去除首尾 `._`，空则回退 "upload"。
func safeFilename(filename string) string {
	name := filename
	if name == "" {
		name = "upload"
	}
	if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
		name = name[idx+1:]
	}
	var sb strings.Builder
	for _, r := range name {
		if isSafeFilenameRune(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	out := strings.Trim(sb.String(), "._")
	if out == "" {
		return "upload"
	}
	return out
}

// safeStem 对齐 legacy _safe_stem（_safe_filename 的 stem）：task 对象键文件名用。
func safeStem(filename string) string {
	s := safeFilename(filename)
	if idx := strings.LastIndexByte(s, '.'); idx > 0 {
		return s[:idx]
	}
	return s
}

func isSafeFilenameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		r == '.' || r == '_' || r == '-' || (r >= 0x4e00 && r <= 0x9fa5)
}

// slugUpper 对齐 legacy _slug：仅保留 A-Za-z0-9，大写，空则回退。
func slugUpper(value, fallback string) string {
	var sb strings.Builder
	for _, r := range strings.ToUpper(value) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		}
	}
	if s := sb.String(); s != "" {
		return s
	}
	return fallback
}

func truncate16(s string) string {
	if len(s) > 16 {
		return s[:16]
	}
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func mustUUID() string {
	id, err := auth.NewUUID()
	if err != nil {
		// auth.NewUUID 仅依赖 crypto/rand，失败概率可忽略；防御性回退。
		return "00000000-0000-4000-8000-000000000000"
	}
	return id
}

func i32Ptr(v int32) *int32 { return &v }