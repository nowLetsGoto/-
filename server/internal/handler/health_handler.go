// health_handler.go · GET /healthz 健康检查。
// 职责：报告进程运行时长、数据库连通性与暂存目录可写性，供 compose / systemd 探活。
// 输入：无参数；输出：{status, uptime, dbOk, storageWritable} 包在统一响应外壳里。
// 边界：独立于 /api/v1 前缀，不随 API 版本变化；探活不写业务数据、不触碰 uploads。
package handler

import (
	"context"
	"net/http"
	"os"
	"time"

	"dandelion/server/internal/contract"
	"dandelion/server/internal/repository"
	"dandelion/server/internal/storage"
)

// HealthHandler 持有连接池、路径对象与进程启动时间。
type HealthHandler struct {
	repo    *repository.BuildRepository
	paths   *storage.Paths
	started time.Time
}

func NewHealthHandler(repo *repository.BuildRepository, paths *storage.Paths) *HealthHandler {
	return &HealthHandler{repo: repo, paths: paths, started: time.Now()}
}

// Health 返回 200 统一响应；探测本身 2 秒超时，DB 不可用时仍返回 200 但 dbOk=false，由运维侧判读。
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dbOK := h.repo.DB().PingContext(ctx) == nil
	return OK(w, contract.HealthStatus{
		Status:           "ok",
		Uptime:           int64(time.Since(h.started).Seconds()),
		DbOK:             dbOK,
		StorageWritable:  probeWritable(h.paths.TmpDir()),
	})
}

// probeWritable 在 tmp 目录创建并立即删除一个临时文件，验证服务对存储卷有写权限。
func probeWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".health-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
