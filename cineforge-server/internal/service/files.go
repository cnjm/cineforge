package service

// P3f-b 文件域服务（对齐 legacy app/api/routes/files.py + app/services/storage.py）：
//   - POST /files/task-upload：行锁/守卫逐字 → 扩展名白名单 → task 对象键 + file_code → MinIO Put
//     → files 登记 → 操作日志（DB 失败回滚删除 MinIO 对象）
//   - content/stream/download：`_authorized_file` + Range/206/416 + 服务端流式代理
//   - playback-url/download-url：HMAC 文件 token（`file-playback:` 前缀 + auth_secret）
//
// Token 与 legacy 字节兼容：payload JSON sort_keys + urlsafe b64 无 padding，`{body}.{signature}`。

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/storage"
)

// maxFileTokenTTLSeconds 对齐 MAX_PLAYBACK/MAX_DOWNLOAD_TOKEN_TTL_SECONDS。
const maxFileTokenTTLSeconds = 5 * 60

// FileService 文件域服务。
type FileService struct {
	pool         *pgxpool.Pool
	files        *repository.Files
	projects     *repository.Projects
	tasks        *repository.Tasks
	client       *storage.Client
	bucket       string
	secret       string
	playbackTTLS int
}

// NewFileService 构造文件域服务。secret 为 auth_secret（文件 token HMAC 密钥）。
func NewFileService(pool *pgxpool.Pool, files *repository.Files, projects *repository.Projects,
	tasks *repository.Tasks, client *storage.Client, bucket, secret string, playbackTTLSeconds int) *FileService {
	return &FileService{
		pool: pool, files: files, projects: projects, tasks: tasks,
		client: client, bucket: bucket, secret: secret, playbackTTLS: playbackTTLSeconds,
	}
}

// storer 返回对象存储客户端；MinIO 未配置（开发占位）时返回错误 → 写入/读取按 500 契约。
func (s *FileService) storer() (*storage.Client, error) {
	if s.client == nil {
		return nil, errors.New("object storage not configured")
	}
	return s.client, nil
}

// PlaybackTTLSeconds 对齐 `min(max(1, settings.file_playback_token_ttl_seconds), 300)`。
func (s *FileService) PlaybackTTLSeconds() int {
	ttl := s.playbackTTLS
	if ttl < 1 {
		ttl = 1
	}
	if ttl > maxFileTokenTTLSeconds {
		ttl = maxFileTokenTTLSeconds
	}
	return ttl
}

// Stat 对象大小（路由层流式响应用）。
func (s *FileService) Stat(ctx context.Context, bucket, objectKey string) (int64, error) {
	client, err := s.storer()
	if err != nil {
		return 0, err
	}
	return client.Stat(ctx, bucket, objectKey)
}

// Open 打开对象读流（length>0 → Range；offset>0 且 length==0 → 读到末尾；全 0 → 整对象）。
func (s *FileService) Open(ctx context.Context, bucket, objectKey string, offset, length int64) (io.ReadCloser, error) {
	client, err := s.storer()
	if err != nil {
		return nil, err
	}
	return client.Open(ctx, bucket, objectKey, offset, length)
}

// RemoveObject 删除对象（DB 登记失败时的 MinIO 回滚）。
func (s *FileService) RemoveObject(ctx context.Context, bucket, objectKey string) error {
	client, err := s.storer()
	if err != nil {
		return err
	}
	return client.Remove(ctx, bucket, objectKey)
}

// ---- 上传 ----

// UploadTaskFile 对齐 files.py upload_task_result_file + storage.upload_task_file。
// 守卫顺序逐字：get_task_model(404/403) → locked(400) → status(400) → 扩展名(400) → MinIO 写入。
func (s *FileService) UploadTaskFile(ctx context.Context, actor repository.TaskActor, taskID string,
	raw io.Reader, filename, contentType string, fileSize *int64) (*model.Files, error) {
	task, err := s.tasks.GetTaskModel(ctx, s.pool, taskID, actor)
	if err != nil {
		return nil, mapTaskError(err, "Task not found", "Task permission denied")
	}
	locked, _, depCodes, err := s.tasks.TaskDependencyState(ctx, s.pool, task)
	if err != nil {
		return nil, err
	}
	if locked {
		labels := "前置任务"
		if len(depCodes) > 0 {
			labels = strings.Join(depCodes, "、")
		}
		return nil, badRequest400(labels + " 尚未完成，当前任务暂未解锁。")
	}
	if task.Status == "reviewing" || task.Status == "submitted" || task.Status == "completed" {
		return nil, badRequest400("当前任务状态不能继续上传候选成果。")
	}

	fileType := storageFileTypeForTask(task.TaskType)
	ext := fileExtNoDot(filename)
	allowed := allowedExtensionsFor(fileType)
	if !allowed[ext] {
		list := make([]string, 0, len(allowed))
		for e := range allowed {
			list = append(list, e)
		}
		sort.Strings(list)
		return nil, badRequest400(fmt.Sprintf("不支持的文件类型 .%s，当前任务允许：%s", ext, strings.Join(list, ", ")))
	}

	parts, err := s.files.TaskProjectParts(ctx, task.ProjectId, task.StoryboardId, task.AssetId)
	if err != nil {
		if repository.IsNotFoundError(err) {
			return nil, notFound404("Project not found")
		}
		return nil, err
	}
	versionNo, err := s.files.NextTaskFileVersion(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	objectKey := taskObjectKey(task, parts, versionNo, filename, ext)
	bucket := s.bucket

	rawSize := int64(-1)
	if fileSize != nil {
		rawSize = *fileSize
	}
	resolvedContentType := contentType
	if resolvedContentType != "" {
		resolvedContentType = strings.TrimSpace(resolvedContentType)
	}
	if resolvedContentType == "" {
		resolvedContentType = guessContentType(filename)
	}

	client, err := s.storer()
	if err != nil {
		return nil, err
	}
	if err := client.Put(ctx, bucket, objectKey, raw, rawSize, resolvedContentType); err != nil {
		return nil, fmt.Errorf("upload task file %q: %w", filename, err)
	}
	f := &model.Files{
		ID:         mustUUID(),
		FileCode:   strPtr(taskFileCode(parts, task, versionNo)),
		Bucket:     bucket,
		ObjectKey:  objectKey,
		FileName:   safeFilename(filename),
		MimeType:   &resolvedContentType,
		FileSize:   i64ToI32Ptr(fileSize),
		UploadedBy: &actor.ID,
		ProjectId:  &task.ProjectId,
		EpisodeId:  task.EpisodeId,
		TaskId:     &task.ID,
	}
	if err := s.projects.AddFile(ctx, f); err != nil {
		_ = s.RemoveObject(ctx, bucket, objectKey) // 对齐 legacy 异常路径回滚 MinIO 对象
		return nil, fmt.Errorf("register task file: %w", err)
	}
	_ = s.projects.RegisterOperationLog(ctx, task.ProjectId, actor.ID, "file", f.ID, "file_uploaded", map[string]any{
		"task_id":    task.ID,
		"bucket":     bucket,
		"object_key": objectKey,
		"file_name":  f.FileName,
	})
	return f, nil
}

// AuthorizedFile 对齐 files.py _authorized_file：404/403 → is_company_asset_library_file 放行。
func (s *FileService) AuthorizedFile(ctx context.Context, fileID string, actor repository.TaskActor) (*model.Files, error) {
	f, err := s.files.FindFileByID(ctx, fileID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, notFound404("File not found")
	}
	if f.TaskId == nil {
		return nil, forbidden403("File permission denied")
	}
	_, err = s.tasks.GetTaskModel(ctx, s.pool, *f.TaskId, actor)
	if err != nil {
		if repository.IsNotFoundError(err) || repository.IsForbiddenError(err) {
			lib, lerr := s.files.IsCompanyAssetLibraryFile(ctx, f.ID)
			if lerr != nil {
				return nil, lerr
			}
			if lib {
				return f, nil
			}
			return nil, forbidden403("File permission denied")
		}
		return nil, err
	}
	return f, nil
}

// ---- 对象键 / 文件码（逐字对齐 storage.py） ----

// taskObjectKey 对齐 _task_object_key。
func taskObjectKey(task *model.Tasks, parts *repository.TaskProjectParts, versionNo int32, filename, ext string) string {
	projectPrefix := truncate16(slugUpper(firstNonEmptyStr(ptrOr(parts.ProjectPrefix, ""), ptrOr(parts.ProjectNo, ""), parts.ProjectTitle), "RF"))
	episodeCode := "EP00"
	shotCode := "S000"
	if task.StoryboardId != nil {
		episodeCode = fmt.Sprintf("EP%02d", parts.EpisodeNum)
		shotCode = fmt.Sprintf("S%03d", parts.OrderNum)
	}
	taskType := strings.ReplaceAll(task.TaskType, "_", "-")
	variantCode := ""
	if task.VariantPlanId != nil && parts.AssetCode != nil && task.TaskVariant != nil {
		// Variant 文件名是业务标识，与 DB 资产码的项目前缀无关：SC001-V001 / P001-V001。
		businessCode := strings.ToUpper(strings.TrimSpace(*parts.AssetCode))
		projectCode := slugUpper(firstNonEmptyStr(ptrOr(parts.ProjectPrefix, ""), ptrOr(parts.ProjectNo, "")), "")
		if projectCode != "" && strings.HasPrefix(businessCode, projectCode+"-") {
			businessCode = businessCode[len(projectCode)+1:]
		}
		variantCode = fmt.Sprintf("%s-%s", slugUpper(businessCode, "ASSET"), slugUpper(*task.TaskVariant, "V001"))
	}
	taskCode := variantCode
	if taskCode == "" {
		taskCode = "TASK-" + taskID8(task.ID)
	}
	versionCode := fmt.Sprintf("V%03d", versionNo)
	now := time.Now().UTC()
	timestamp := now.Format("20060102150405") + fmt.Sprintf("%06d", now.Nanosecond()/1000)
	uploadID := uuidHex(12)
	stem := "upload"
	if variantCode != "" {
		stem = "result"
	} else {
		stem = cut(safeStem(filename), 48)
		if stem == "" {
			stem = "upload"
		}
	}
	filenamePart := fmt.Sprintf("%s-%s-%s-%s-%s-%s-%s-%s-%s.%s",
		projectPrefix, episodeCode, shotCode, taskType, taskCode, versionCode, timestamp, uploadID, stem, ext)
	folder := taskType
	if variantCode != "" {
		folder = "asset-variants"
	}
	return strings.Join([]string{projectPrefix, episodeCode, folder, taskCode, versionCode, filenamePart}, "/")
}

// taskFileCode 对齐 storage._file_code（前缀只取 project.project_prefix，缺省 "RF"）。
func taskFileCode(parts *repository.TaskProjectParts, task *model.Tasks, versionNo int32) string {
	prefix := truncate16(slugUpper(ptrOr(parts.ProjectPrefix, "RF"), "RF"))
	taskTypeUpper := strings.ToUpper(strings.ReplaceAll(task.TaskType, "_", "-"))
	return fmt.Sprintf("%s-%s-%s-V%03d", prefix, taskTypeUpper, strings.ToUpper(uuidHex(8)), versionNo)
}

// ---- 扩展名白名单（对齐 storage.py storage_file_type_for_task / allowed_extensions_for） ----

var (
	imageExts    = map[string]bool{"jpg": true, "jpeg": true, "png": true}
	videoExts    = map[string]bool{"mp4": true, "mov": true}
	audioExts    = map[string]bool{"wav": true, "mp3": true}
	documentExts = map[string]bool{"docx": true, "pdf": true, "txt": true, "md": true}
)

func storageFileTypeForTask(taskType string) string {
	switch taskType {
	case "audio":
		return "audio"
	case "asset", "text_to_image":
		return "image"
	case "storyboard_shot":
		return "media"
	case "image_to_video", "video_generation", "assembly", "final_output":
		return "video"
	}
	return "reference"
}

func allowedExtensionsFor(fileType string) map[string]bool {
	switch fileType {
	case "image", "text_to_image", "storyboard_image":
		return extSet(imageExts)
	case "video", "image_to_video", "video_generation", "final_video":
		return extSet(videoExts)
	case "audio", "voice", "music", "theme_music", "background_music":
		return extSet(audioExts)
	case "document", "script", "reference":
		return extSet(documentExts)
	case "media":
		return extSet(imageExts, videoExts)
	}
	return extSet(imageExts, videoExts, audioExts, documentExts)
}

func extSet(sets ...map[string]bool) map[string]bool {
	out := map[string]bool{}
	for _, s := range sets {
		for k := range s {
			out[k] = true
		}
	}
	return out
}

// fileExtNoDot 对齐 storage._extension（无前导点，空 → "bin"）。
func fileExtNoDot(filename string) string {
	return strings.TrimPrefix(extensionOf(filename), ".")
}

// guessContentType 对齐 `content_type or mimetypes.guess_type(...)[0] or "application/octet-stream"`。
func guessContentType(filename string) string {
	if ct := guessMimeByExt(fileExtNoDot(filename)); ct != "" {
		return ct
	}
	if ct := mime.TypeByExtension(extensionOf(filename)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func guessMimeByExt(ext string) string {
	switch strings.ToLower(ext) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "mp4":
		return "video/mp4"
	case "mov":
		return "video/quicktime"
	case "wav":
		return "audio/x-wav"
	case "mp3":
		return "audio/mpeg"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "pdf":
		return "application/pdf"
	case "txt":
		return "text/plain"
	case "md":
		return "text/markdown"
	}
	return ""
}

// ---- 文件播放/下载 token（逐字对齐 files.py _create_file_token / _verify_file_token） ----

var (
	errInvalidPlaybackToken = &StatusError{Status: 401, Detail: "Invalid playback token"}
	errPlaybackTokenExpired = &StatusError{Status: 401, Detail: "Playback token expired"}
)

// FileTokenPayload 解析后的文件 token 载荷。
type FileTokenPayload struct {
	Purpose      string
	FileID       string
	UserID       string
	ExpiresAt    int64
	DownloadName *string
}

// CreateFileToken 对齐 _create_file_token（payload json.Marshal 按键排序 == sort_keys=True）。
func (s *FileService) CreateFileToken(purpose, fileID, userID string, expiresAt time.Time, downloadName *string) string {
	payload := map[string]any{
		"purpose": purpose,
		"file_id": fileID,
		"user_id": userID,
		"exp":     expiresAt.Unix(),
	}
	if downloadName != nil && *downloadName != "" {
		payload["download_name"] = *downloadName
	}
	raw, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + s.playbackSignature(body)
}

// VerifyFileToken 对齐 _verify_file_token_payload；detail 逐字（Invalid playback token / Playback token expired）。
func (s *FileService) VerifyFileToken(token, expectedFileID, purpose string) (*FileTokenPayload, error) {
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errInvalidPlaybackToken
	}
	if !hmac.Equal([]byte(s.playbackSignature(body)), []byte(sig)) {
		return nil, errInvalidPlaybackToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, errInvalidPlaybackToken
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, errInvalidPlaybackToken
	}
	fileID, ok1 := payload["file_id"].(string)
	userID, ok2 := payload["user_id"].(string)
	purposeVal, ok3 := payload["purpose"].(string)
	exp, ok4 := payload["exp"].(float64)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, errInvalidPlaybackToken
	}
	if purposeVal != purpose || fileID != expectedFileID {
		return nil, errInvalidPlaybackToken
	}
	if int64(exp) <= time.Now().UTC().Unix() {
		return nil, errPlaybackTokenExpired
	}
	out := &FileTokenPayload{Purpose: purposeVal, FileID: fileID, UserID: userID, ExpiresAt: int64(exp)}
	if v, ok := payload["download_name"].(string); ok {
		out.DownloadName = &v
	}
	return out, nil
}

// playbackSignature 对齐 _playback_signature：hmac_sha256(auth_secret, "file-playback:" + body)。
func (s *FileService) playbackSignature(body string) string {
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte("file-playback:" + body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SafeDownloadName 对齐 files.py _safe_download_name：对 `[<>:"/\\|?*\r\n]+` 折叠为 `_`，
// 去除首尾 ` ._`，[:240]。
func SafeDownloadName(value string) string {
	disallowed := func(r rune) bool {
		return r == '<' || r == '>' || r == ':' || r == '"' || r == '/' || r == '\\' ||
			r == '|' || r == '?' || r == '*' || r == '\r' || r == '\n'
	}
	var sb strings.Builder
	inRun := false
	for _, r := range value {
		if disallowed(r) {
			if !inRun {
				sb.WriteRune('_')
				inRun = true
			}
			continue
		}
		inRun = false
		sb.WriteRune(r)
	}
	out := strings.Trim(sb.String(), " ._")
	return cut(out, 240)
}

// ---- Range 解析（逐字对齐 files.py _parse_byte_range） ----

// ByteRange 闭区间 [Start, End]。
type ByteRange struct {
	Start int64
	End   int64
}

// Length 表示区间长度。
func (r ByteRange) Length() int64 { return r.End - r.Start + 1 }

var byteRangeRegexp = regexp.MustCompile(`(?i)^bytes=(\d*)-(\d*)$`)

// ParseByteRange 解析 Range 头；ok=false 表示非法（→ 416 "bytes */{size}"）。
func ParseByteRange(header string, size int64) (ByteRange, bool) {
	m := byteRangeRegexp.FindStringSubmatch(strings.TrimSpace(header))
	var zero ByteRange
	if m == nil || size <= 0 {
		return zero, false
	}
	startText, endText := m[1], m[2]
	if startText == "" && endText == "" {
		return zero, false
	}
	if startText == "" {
		suffixLen := rangeInt(endText)
		if suffixLen <= 0 {
			return zero, false
		}
		start := size - suffixLen
		if start < 0 {
			start = 0
		}
		return ByteRange{Start: start, End: size - 1}, true
	}
	start := rangeInt(startText)
	if start >= size {
		return zero, false
	}
	end := size - 1
	if endText != "" {
		if v := rangeInt(endText); v < end {
			end = v
		}
	}
	if end < start {
		return zero, false
	}
	return ByteRange{Start: start, End: end}, true
}

// rangeInt 对齐 _range_int：非数字 → 非法（返回 -1 由调用方判定）。
func rangeInt(value string) int64 {
	var n int64
	for _, r := range value {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// ---- 工具 ----

// uuidHex 对齐 `uuid.uuid4().hex[:n]`（小写 hex）。
func uuidHex(n int) string {
	id := mustUUID()
	return strings.ReplaceAll(id, "-", "")[:n]
}

// taskID8 对齐 `str(task_id)[:8]`。
func taskID8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// cut 截断为前 n 个 rune（对齐 Python `[:n]` 的码点语义）。
func cut(s string, n int) string {
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func i64ToI32Ptr(v *int64) *int32 {
	if v == nil {
		return nil
	}
	out := int32(*v)
	return &out
}