package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"gps-archive-tool/internal/config"
	"gps-archive-tool/internal/handlers"
	"gps-archive-tool/internal/services"
)

func main() {
	// 获取配置路径
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	configPath := filepath.Join(execDir, "config.json")

	// 也支持当前工作目录下的config.json
	if _, err := os.Stat("config.json"); err == nil {
		configPath = "config.json"
	}

	// 初始化配置
	err := config.Init(configPath)
	if err != nil {
		log.Fatalf("初始化配置失败: %v", err)
	}
	log.Printf("配置文件路径: %s", configPath)

	// 创建服务
	taskManager := services.NewTaskManager()
	vehicleService := services.NewVehicleService()
	archiveService := services.NewArchiveService(taskManager)
	bindLogService := services.NewBindLogService()
	cosService := services.NewCOSService()

	// 创建处理器
	h := handlers.NewHandler(vehicleService, archiveService, taskManager, bindLogService, cosService)

	// 启动CSV过滤器管理器(自动恢复未完成任务)
	csvFilterMgr := h.GetCSVFilterManager()
	csvFilterMgr.ResumeOnStartup(".")
	log.Println("CSV过滤器管理器已启动")

	// 创建Gin路由
	r := gin.Default()

	// CORS配置 - 允许前端跨域访问
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// API路由
	api := r.Group("/api")
	{
		// 健康检查
		api.GET("/health", h.Health)

		// 配置管理
		api.GET("/config", h.GetConfig)
		api.PUT("/config", h.UpdateConfig)
		api.POST("/config", h.SaveFullConfig)

		// 车辆查询
		api.POST("/vehicle/query", h.QueryVehicle)
		api.POST("/vehicle/batch-query", h.BatchQueryVehicle)

		// TID历史查询
		api.POST("/vehicle/tid-history", h.QueryTIDHistory)
		api.GET("/vehicle/tid-history", h.QueryTIDHistory)

		// 归档文件管理
		api.GET("/archive/files", h.ListArchiveFiles)

		// 过滤任务
		api.POST("/filter/start", h.StartFilter)
		api.GET("/filter/task/:taskId", h.GetTaskStatus)
		api.GET("/filter/tasks", h.ListTasks)
		api.DELETE("/filter/task/:taskId", h.DeleteTask)

		// COS存储桶文件管理
		api.GET("/cos/files", h.ListCOSFiles)

		// 过滤任务(COS管道)
		api.POST("/filter/cos-task", h.CreateCOSFilterTask)

		// 设备绑定流水查询(t_bind_log)
		api.POST("/bindlog/query", h.QueryBindLog)

		// 导入绑定流水CSV文件，按时间范围过滤TID
		api.POST("/filter/import-csv", h.ImportCSV)

		// COS文件下载(返回本地路径用于CSV过滤)
		api.POST("/cos/download", h.DownloadCOSFile)

		// CSV过滤任务(直接从gzip SQL文件按CSV绑定段过滤)
		api.POST("/filter/csv-upload", h.UploadCSVFile)
		api.POST("/filter/csv-submit", h.StartCSVFilter)
		api.GET("/filter/csv-tasks", h.ListCSVFilterTasks)
		api.GET("/filter/csv-cancel", h.CancelCSVFilterTask)
	}

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("正在关闭服务...")
		vehicleService.Close()
		bindLogService.Close()
		os.Exit(0)
	}()

	// 启动服务器
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}

	fmt.Printf(`
╔══════════════════════════════════════════════════════╗
║          GPS归档数据过滤工具 - 后端服务              ║
╠══════════════════════════════════════════════════════╣
║  服务地址: http://localhost:%s                      ║
║  健康检查: http://localhost:%s/api/health           ║
╠══════════════════════════════════════════════════════╣
║  功能说明:                                          ║
║  1. 从归档文件过滤指定TID的GPS数据                  ║
║  2. 导入临时MySQL数据库                             ║
║  3. 导出为TID命名的SQL文件                          ║
║  4. 支持批量车辆查询及TID绑定历史查询               ║
╚══════════════════════════════════════════════════════╝
`, port, port)

	err = r.Run(":" + port)
	if err != nil {
		log.Fatalf("启动服务器失败: %v", err)
	}
}
