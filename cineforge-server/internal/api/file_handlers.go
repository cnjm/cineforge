package api

// P3f-b 文件域 handlers（对齐 legacy app/api/routes/files.py）：
//   - POST /files/task-upload        上传任务候选（multipart，逐字守卫 → 201 FileUploadResponse）
//   - GET  /files/{id}/content       代理读取（Bearer + Range/206/416）
//   - GET  /files/{id}/playback-url  签发播放 token（HMAC，同源流式 URL）
//   - GET  /files/{id}/download-url  签发下载 token（可带自定义 file_name）
//   - GET  /files/{id}/stream        播放流（token 校验，无 Bearer）
//   - GET  /files/{id}/download      下载流（token 校验，attachment）
//
// content/playback-url/download-url 走 Bearer；stream/download 走 token query（下载管理器/视频容
// 器无法带 Authorization 头）。token 错误 → 401 "Invalid playback token" / "Playback token expired"。

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"cineforge/server/internal/model"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/service"
)

// registerFiles 注册 /files 路由组。
func (s *Server) registerFiles(g *gin.RouterGroup) {
	files := g.Group("/files")
	files.POST("/task-upload", s.requireAuth(), s.handleTaskUpload)
	files.GET("/:file_id/content", s.requireAuth(), s.handleFileContent)
	files.GET("/:file_id/playback-url", s.requireAuth(), s.handleFilePlaybackURL)
	files.GET("/:file_id/download-url", s.requireAuth(), s.handleFileDownloadURL)
	files.GET("/:file_id/download", s.handleFileDownload)
	files.GET("/:file_id/stream", s.handleFileStream)
}

// ---- POST /files/task-upload ----

func (s *Server) handleTaskUpload(c *gin.Context) {
	user := userFromContext(c)
	taskID, fileSize, filename, contentType, filePart, err := parseTaskUploadParts(c.Request)
	if err != nil || taskID == "" || filePart == nil {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	if filename == "" {
		filename = "upload.bin"
	}
	f, err := s.files.UploadTaskFile(c.Request.Context(),
		repository.TaskActor{ID: user.ID, Role: user.Role},
		taskID, filePart, filename, contentType, fileSize)
	if err != nil {
		writeServiceError(c, "任务文件上传", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":         f.ID,
		"file_code":  nilOr(f.FileCode),
		"bucket":     f.Bucket,
		"object_key": f.ObjectKey,
		"file_name":  f.FileName,
		"mime_type":  f.MimeType,
		"file_size":  f.FileSize,
		"url":        "minio://" + f.Bucket + "/" + f.ObjectKey,
	})
}

// parseTaskUploadParts 手动解析 multipart（gin/binding 丢弃 part 的 Content-Type，无法还原
// `file.content_type`）。text 字段读入内存；file part 只捕获流不消费，直接交给 MinIO 流式上传。
func parseTaskUploadParts(r *http.Request) (taskID string, fileSize *int64, filename, contentType string, file *multipart.Part, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", nil, "", "", nil, err
	}
	for {
		p, nextErr := mr.NextPart()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return "", nil, "", "", nil, nextErr
		}
		switch p.FormName() {
		case "task_id":
			b, _ := io.ReadAll(io.LimitReader(p, 4096))
			taskID = strings.TrimSpace(string(b))
		case "file_size":
			b, _ := io.ReadAll(io.LimitReader(p, 128))
			if v, perr := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); perr == nil {
				fileSize = &v
			}
		case "file":
			// file 是表单最后一个 part：捕获后立即返回，避免继续 NextPart 消费文件流。
			if file == nil {
				filename = p.FileName()
				contentType = p.Header.Get("Content-Type")
				file = p
			}
			return taskID, fileSize, filename, contentType, file, nil
		}
	}
	return taskID, fileSize, filename, contentType, file, nil
}

// ---- GET /files/{id}/content ----

func (s *Server) handleFileContent(c *gin.Context) {
	user := userFromContext(c)
	fileID := c.Param("file_id")
	f, err := s.files.AuthorizedFile(c.Request.Context(), fileID,
		repository.TaskActor{ID: user.ID, Role: user.Role})
	if err != nil {
		writeServiceError(c, "文件读取", err)
		return
	}
	s.streamStoredFile(c, f, c.GetHeader("Range"), false, "")
}

// ---- GET /files/{id}/stream ----

func (s *Server) handleFileStream(c *gin.Context) {
	fileID := c.Param("file_id")
	payload, err := s.files.VerifyFileToken(c.Query("token"), fileID, "file-playback")
	if err != nil {
		writeServiceError(c, "文件播放", err)
		return
	}
	user, err := s.users.FindByID(c.Request.Context(), payload.UserID)
	if err != nil {
		internalError(c, "无法加载用户信息")
		return
	}
	if user == nil || !user.IsActive {
		unauthorized401(c, "Playback user disabled or not found")
		return
	}
	f, err := s.files.AuthorizedFile(c.Request.Context(), fileID,
		repository.TaskActor{ID: user.ID, Role: user.Role})
	if err != nil {
		writeServiceError(c, "文件播放", err)
		return
	}
	s.streamStoredFile(c, f, c.GetHeader("Range"), false, "")
}

// ---- GET /files/{id}/download ----

func (s *Server) handleFileDownload(c *gin.Context) {
	fileID := c.Param("file_id")
	payload, err := s.files.VerifyFileToken(c.Query("token"), fileID, "file-download")
	if err != nil {
		writeServiceError(c, "文件下载", err)
		return
	}
	user, err := s.users.FindByID(c.Request.Context(), payload.UserID)
	if err != nil {
		internalError(c, "无法加载用户信息")
		return
	}
	if user == nil || !user.IsActive {
		unauthorized401(c, "Download user disabled or not found")
		return
	}
	f, err := s.files.AuthorizedFile(c.Request.Context(), fileID,
		repository.TaskActor{ID: user.ID, Role: user.Role})
	if err != nil {
		writeServiceError(c, "文件下载", err)
		return
	}
	attachmentName := ""
	if payload.DownloadName != nil {
		attachmentName = service.SafeDownloadName(*payload.DownloadName)
	}
	s.streamStoredFile(c, f, "", true, attachmentName)
}

// ---- GET /files/{id}/playback-url ----

func (s *Server) handleFilePlaybackURL(c *gin.Context) {
	user := userFromContext(c)
	fileID := c.Param("file_id")
	if _, err := s.files.AuthorizedFile(c.Request.Context(), fileID,
		repository.TaskActor{ID: user.ID, Role: user.Role}); err != nil {
		writeServiceError(c, "文件播放", err)
		return
	}
	ttl := s.files.PlaybackTTLSeconds()
	expiresAt := time.Now().UTC().Truncate(time.Microsecond).Add(time.Duration(ttl) * time.Second)
	token := s.files.CreateFileToken("file-playback", fileID, user.ID, expiresAt, nil)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"url":                fmt.Sprintf("/api/files/%s/stream?token=%s", fileID, token),
		"expires_at":         expiresAt,
		"expires_in_seconds": ttl,
	})
}

// ---- GET /files/{id}/download-url ----

func (s *Server) handleFileDownloadURL(c *gin.Context) {
	user := userFromContext(c)
	fileID := c.Param("file_id")
	if _, err := s.files.AuthorizedFile(c.Request.Context(), fileID,
		repository.TaskActor{ID: user.ID, Role: user.Role}); err != nil {
		writeServiceError(c, "文件下载", err)
		return
	}
	fileName := c.Query("file_name")
	if len([]rune(fileName)) > 240 {
		unprocessableEntity(c, "请求体格式错误")
		return
	}
	downloadName := service.SafeDownloadName(fileName)
	var dn *string
	if downloadName != "" {
		dn = &downloadName
	}
	ttl := s.files.PlaybackTTLSeconds()
	expiresAt := time.Now().UTC().Truncate(time.Microsecond).Add(time.Duration(ttl) * time.Second)
	token := s.files.CreateFileToken("file-download", fileID, user.ID, expiresAt, dn)
	c.Header("Cache-Control", "no-store")
	resp := gin.H{
		"url":                fmt.Sprintf("/api/files/%s/download?token=%s", fileID, token),
		"expires_at":         expiresAt,
		"expires_in_seconds": ttl,
	}
	if dn != nil {
		resp["file_name"] = dn
	}
	c.JSON(http.StatusOK, resp)
}

// ---- 流式代理（对齐 _stored_file_response）----

func (s *Server) streamStoredFile(c *gin.Context, f *model.Files, rangeHeader string, attachment bool, attachmentName string) {
	ctx := c.Request.Context()
	size, err := s.files.Stat(ctx, f.Bucket, f.ObjectKey)
	if err != nil {
		writeServiceError(c, "文件读取", err)
		return
	}
	// 对齐 legacy：Range 头缺失 → 200 全量；存在但非法 → 416 "bytes */{size}"。
	rangeHeader = strings.TrimSpace(rangeHeader)
	if rangeHeader == "" {
		reader, err := s.files.Open(ctx, f.Bucket, f.ObjectKey, 0, 0)
		if err != nil {
			writeServiceError(c, "文件读取", err)
			return
		}
		defer reader.Close()
		c.DataFromReader(http.StatusOK, size, mediaTypeOf(f), reader,
			extraContentHeaders(service.ByteRange{}, 0, attachment, attachmentName, f))
		return
	}
	byteRange, ok := service.ParseByteRange(rangeHeader, size)
	if !ok {
		c.DataFromReader(http.StatusRequestedRangeNotSatisfiable, 0, "application/octet-stream",
			bytes.NewReader(nil), map[string]string{
				"Accept-Ranges": "bytes",
				"Content-Range": fmt.Sprintf("bytes */%d", size),
				"Cache-Control": "private, max-age=300",
			})
		return
	}
	status := http.StatusPartialContent
	length := byteRange.Length()
	reader, err := s.files.Open(ctx, f.Bucket, f.ObjectKey, byteRange.Start, length)
	if err != nil {
		writeServiceError(c, "文件读取", err)
		return
	}
	defer reader.Close()
	c.DataFromReader(status, length, mediaTypeOf(f), reader, extraContentHeaders(byteRange, size, attachment, attachmentName, f))
}

// mediaTypeOf 对齐 `record.mime_type or "application/octet-stream"`。
func mediaTypeOf(f *model.Files) string {
	if f.MimeType != nil && *f.MimeType != "" {
		return *f.MimeType
	}
	return "application/octet-stream"
}

// extraContentHeaders 组装预留头（Accept-Ranges / Cache-Control / Vary / Content-Range / Content-Disposition）。
func extraContentHeaders(byteRange service.ByteRange, size int64, attachment bool, attachmentName string, f *model.Files) map[string]string {
	headers := map[string]string{
		"Accept-Ranges": "bytes",
		"Cache-Control": "private, max-age=300",
		"Vary":          "Range",
	}
	if size > 0 {
		headers["Content-Range"] = fmt.Sprintf("bytes %d-%d/%d", byteRange.Start, byteRange.End, size)
	}
	if attachment {
		name := service.SafeDownloadName(attachmentName)
		if name == "" {
			name = service.SafeDownloadName(f.FileName)
		}
		if name == "" {
			name = "download.bin"
		}
		headers["Content-Disposition"] = "attachment; filename*=UTF-8''" + percentEncodeRFC3986(name)
	}
	return headers
}

// percentEncodeRFC3986 对齐 Python urllib.parse.quote(name, safe='')：
// 除 A-Za-z0-9._-~ 外的所有字节均 %XX 大写编码。
func percentEncodeRFC3986(s string) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') ||
			b == '-' || b == '.' || b == '_' || b == '~' {
			sb.WriteByte(b)
		} else {
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[b>>4])
			sb.WriteByte(hexDigits[b&0xf])
		}
	}
	return sb.String()
}

func nilOr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}