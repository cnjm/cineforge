package api

// 统一错误响应（FastAPI 契约：HTTPException → {"detail": "..."}）。
// 前端 api.ts 从 body.detail 取错误文案，401 一律判定登录失效。

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// errorJSON 写 {"detail": detail}（detail 逐字对齐 legacy）。
func errorJSON(c *gin.Context, status int, detail string) {
	c.AbortWithStatusJSON(status, gin.H{"detail": detail})
}

// badRequest400 (legacy ValueError → 400，detail 为异常字符串)
func badRequest400(c *gin.Context, detail string) {
	errorJSON(c, http.StatusBadRequest, detail)
}

// unauthorized401
func unauthorized401(c *gin.Context, detail string) {
	errorJSON(c, http.StatusUnauthorized, detail)
}

// forbidden403
func forbidden403(c *gin.Context, detail string) {
	errorJSON(c, http.StatusForbidden, detail)
}

// conflict409（legacy HTTPException(409, ...) → ValueError → 409）
func conflict409(c *gin.Context, detail string) {
	errorJSON(c, http.StatusConflict, detail)
}

// notFound404
func notFound404(c *gin.Context, detail string) {
	errorJSON(c, http.StatusNotFound, detail)
}

// internalError500
func internalError(c *gin.Context, detail string) {
	errorJSON(c, http.StatusInternalServerError, detail)
}

// unprocessableEntity422（legacy Pydantic 校验失败 → 422；detail 简化为字符串文案）
func unprocessableEntity(c *gin.Context, detail string) {
	errorJSON(c, http.StatusUnprocessableEntity, detail)
}
