package api

// /api/admin/* 端点（对齐 legacy app/api/routes/admin.py，全部 director/admin）：
//
//   - GET  /admin/overview → stats + overdue_tasks + recent_activities + recent_readings
//   - GET  /admin/probe    → 27 张表 counts + recent_projects/agent_runs/operation_logs

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *Server) registerAdmin(g *gin.RouterGroup) {
	a := g.Group("/admin", s.requireAuth(), s.requireDirectorOrAdmin())
	a.GET("/overview", s.handleAdminOverview)
	a.GET("/probe", s.handleAdminProbe)
}

func (s *Server) handleAdminOverview(c *gin.Context) {
	overview, err := s.admin.Overview(c.Request.Context())
	if err != nil {
		internalError(c, "管理概览加载失败")
		return
	}
	c.JSON(http.StatusOK, overview)
}

func (s *Server) handleAdminProbe(c *gin.Context) {
	probe, err := s.admin.Probe(c.Request.Context())
	if err != nil {
		internalError(c, "管理探针查询失败")
		return
	}
	c.JSON(http.StatusOK, probe)
}
