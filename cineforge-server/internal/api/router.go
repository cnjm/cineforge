package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"cineforge/server/internal/agentclient"
	"cineforge/server/internal/casbin"
	"cineforge/server/internal/config"
	"cineforge/server/internal/db"
	"cineforge/server/internal/repository"
	"cineforge/server/internal/service"
	"cineforge/server/internal/storage"
)

// Server 持有各业务域 handler 共享的依赖（config + db pool + 域服务）。
type Server struct {
	cfg   *config.Config
	pool  *pgxpool.Pool
	users *repository.Users
	perm  *repository.Permissions

	enforcer *casbin.Enforcer
	authSvc  *service.AuthService
	userSvc  *service.UserService
	projects *service.ProjectService
	tasks    *service.TaskService
	files    *service.FileService
	assets   *service.AssetService
	notify   *service.NotifyService
	admin    *service.AdminService
	agent    *agentclient.Client
}

func NewRouter(cfg *config.Config, pool *pgxpool.Pool) *gin.Engine {
	srv := &Server{cfg: cfg, pool: pool}
	srv.users = repository.NewUsers(pool)
	srv.perm = repository.NewPermissions(pool)
	srv.enforcer = casbin.New(pool)
	srv.authSvc = service.NewAuthService(srv.users, cfg)
	srv.userSvc = service.NewUserService(srv.users)
	projectsRepo := repository.NewProjects(pool)
	tasksRepo := repository.NewTasks(pool)
	// MinIO 客户端：先于 projects service 创建，供文件清理（分集删除）与文件域共用。
	fileClient, err := storage.New(cfg.MinIO)
	if err != nil {
		fileClient = nil // MinIO 未配置：文件域读写按 500 契约，playback-url 等 token 接口仍可用
	}
	archiver, err := service.NewMinioArchiver(projectsRepo, cfg.MinIO)
	if err != nil {
		archiver = service.DefaultArchiver()
	}
	srv.agent = agentclient.New(cfg.Agent.Addr)
	srv.projects = service.NewProjectService(pool, projectsRepo, repository.NewUsers(pool), archiver, fileClient, srv.agent)
	srv.tasks = service.NewTaskService(pool, tasksRepo, projectsRepo, srv.agent)
	assetsRepo := repository.NewAssets(pool, projectsRepo)
	srv.assets = service.NewAssetService(assetsRepo)

	srv.files = service.NewFileService(pool, repository.NewFiles(pool), projectsRepo, tasksRepo,
		fileClient, cfg.MinIO.Bucket, cfg.Auth.Secret, cfg.Auth.FilePlaybackTokenTTLSeconds)
	srv.notify = service.NewNotifyService(repository.NewNotifications(pool))
	srv.admin = service.NewAdminService(repository.NewAdmin(pool, tasksRepo))

	engine := gin.Default()

	engine.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	ready := db.ReadyChecker(pool)
	engine.GET("/ready", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		if err := ready(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "reason": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	v1 := engine.Group("/api")
	srv.registerAuth(v1)
	srv.registerUsers(v1)
	srv.registerProjects(v1)
	srv.registerProjectGroupingRoutes(v1)
	srv.registerTasks(v1)
	srv.registerWorkspaceTasks(v1)
	srv.registerProductionBrief(v1)
	srv.registerAgentRuns(v1)
	srv.registerFiles(v1)
	srv.registerAssets(v1)
	srv.registerAdmin(v1)
	srv.registerAgents(v1)
	return engine
}
