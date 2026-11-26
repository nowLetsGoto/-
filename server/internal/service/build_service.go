// Package service 是业务编排层，也是全仓唯一的 URL 生成处与唯一的「上传完成」时点。
// 职责：stage 暂存解析、Create 提交发布（token 校验 → rename → 写库 → 失败补偿 → 生成绝对地址）、
//
//	List/Get 填充地址、ResolveFile 安全定位、下载成功后的计数旁路、tmp 治理。
//
// 输入：contract DTO、StagedFile、config 配置、storage 路径对象、repository 仓储对象。
// 输出：完整 Build / 列表 / 文件引用 / StageResult。
// 边界：不写 SQL（交 repository）、不解析 multipart（交 handler）、不拼存储路径（交 storage）；
//
//	提交时绝不调用 parser、绝不用解析值覆盖用户值，表单即最终真值，仅对空字段兜底。
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dandelion/server/internal/authctx"
	"dandelion/server/internal/config"
	"dandelion/server/internal/contract"
	"dandelion/server/internal/parser"
	"dandelion/server/internal/repository"
	projectrepo "dandelion/server/internal/repository/project"
	"dandelion/server/internal/storage"
)

// StagedFile 描述已由 handler 写入 tmp 的暂存文件。
type StagedFile struct {
	Token            string
	AbsPath          string
	Ext              string
	OriginalFileName string
	// DirectR2Key 非空表示该文件是浏览器直传到 R2 后再回拉的，
	// 对象已在桶里，commit 时可跳过二次上传，直接把 r2_key 指向它。
	DirectR2Key string
}

// stagedLookup 是提交时还原出的暂存文件信息。
type stagedLookup struct {
	absPath          string
	namePath         string
	ext              string
	originalFileName string
	// r2Key 非空表示文件已由浏览器直传到 R2，commit 跳过二次上传。
	r2Key string
}

// BuildNotifier 在发布成功后收到通知。
// tenantID 是包所属公司，实现方据此选择「这家公司」的 Webhook 配置——
// 两家公司的回调地址互不影响，是 P4 隔离的核心。
// 实现方必须自行保证：投递失败只记日志、不阻塞、不影响发布结果（见 service/webhook）。
type BuildNotifier interface {
	NotifyBuild(ctx context.Context, tenantID string, build contract.Build)
}

// BuildService 持有配置、路径、分仓储与可选的通知旁路，由 main.go 装配层注入。
// builds 与 projects 都是分区容器：调用方给出会话模式，服务层据此选取对应的表。
type BuildService struct {
	cfg   config.Config
	paths *storage.Paths
	// r2 为 nil 表示未配置对象存储，所有下载走本机磁盘（与改造前一致）。
	r2 *storage.R2
	// sessions 是浏览器直传 R2 的会话仓储；r2 为 nil 时它也为 nil。
	sessions *repository.UploadSessionRepository
	repos    repository.Partitioned[*repository.BuildRepository]
	projects repository.Partitioned[*projectrepo.Repository]
	notifier BuildNotifier
	shares   repository.Partitioned[shareLookup]
	signer   shareSigner
	authz    buildPermissionChecker
}

func NewBuildService(
	cfg config.Config,
	paths *storage.Paths,
	r2 *storage.R2,
	sessions *repository.UploadSessionRepository,
	repos repository.Partitioned[*repository.BuildRepository],
	projects repository.Partitioned[*projectrepo.Repository],
	notifier BuildNotifier,
	shares repository.Partitioned[*repository.BuildShareRepository],
	authz buildPermissionChecker,
) *BuildService {
	return &BuildService{cfg: cfg, paths: paths, r2: r2, sessions: sessions, repos: repos, projects: projects, notifier: notifier,
		shares: repository.NewPartitioned[shareLookup](shares.For(contract.ModeTeam), shares.For(contract.ModeSolo)),
		signer: newShareSigner(cfg.AuthSecret), authz: authz}
}

// resolveProject 把 key 解析成分区内的项目；key 为空或不存在都按参数错误处理（400）。
// 单人分区的项目按创建者隔离，因此必须带上 ownerUserID——否则会解析到别人的项目。
// 团队分区叠加 scope：包列表与发布的隔离点就在这里——解析不到别公司的项目，
// 后续按 project.ID 查包自然不会串。scope 从 ctx 取（登录态已由 InjectScope 注入）。
func (s *BuildService) resolveProject(ctx context.Context, key, ownerUserID string, mode contract.SessionMode) (contract.Project, error) {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return contract.Project{}, &contract.ValidationError{Message: "请指定项目"}
	}
	return s.projects.For(mode).FindByKey(ctx, trimmed, ownerUserID, authctx.ScopeFrom(ctx))
}

// PurgeTmp 启动时清空 tmp：tmp 是纯临时态，进程重启后不存在「进行中的上传」。
func (s *BuildService) PurgeTmp() error {
	entries, err := os.ReadDir(s.paths.TmpDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读取暂存目录失败: %w", err)
	}
	for _, entry := range entries {
		target := filepath.Join(s.paths.TmpDir(), entry.Name())
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("清空暂存文件失败 %s: %w", target, err)
		}
	}
	return nil
}

// pruneOrphans 每次 stage 前删除超过 1 小时的孤儿文件；零表、零定时任务、零外部组件。
func (s *BuildService) pruneOrphans() {
	entries, err := os.ReadDir(s.paths.TmpDir())
	if err != nil {
		return
	}
	ttl := time.Duration(contract.StageTokenTTLSeconds) * time.Second
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > ttl {
			target := filepath.Join(s.paths.TmpDir(), entry.Name())
			if err := os.RemoveAll(target); err != nil {
				log.Printf("清理过期暂存文件失败 path=%s err=%v", target, err)
			}
		}
	}
}

// Stage 同步解析已落盘的暂存文件。解析失败、包损坏、超时只写 warnings，
// 接口恒返回 StageResult（stage 恒 200），绝不因解析问题阻塞上传。
func (s *BuildService) Stage(_ context.Context, f StagedFile) contract.StageResult {
	s.pruneOrphans()

	file, err := os.Open(f.AbsPath)
	if err != nil {
		log.Printf("解析降级 token=%s 打开暂存文件失败 err=%v", f.Token, err)
		return parseResult(f.Token, contract.ParsedMeta{}, "部分信息未自动识别，可手动修正")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return parseResult(f.Token, contract.ParsedMeta{}, "部分信息未自动识别，可手动修正")
	}

	parsed, err := parseMeta(file, info.Size(), f.Ext)
	if err != nil {
		log.Printf("解析降级 token=%s ext=%s fileName=%s err=%v", f.Token, f.Ext, f.OriginalFileName, err)
		return parseResult(f.Token, parsed, parseWarning(err))
	}
	return parseResult(f.Token, parsed, "")
}

// ParseFrom 用任意 ReaderAt 解析元数据，产出与 Stage 完全相同的 StageResult。
//
// 它为直传场景而生：传入 R2 的 HTTP Range 读取器，即可只读 manifest 所需字节，
// 不必把整包拉回本地。数据来源（本地文件 / 远端对象）由此解耦，解析逻辑只有一份。
func (s *BuildService) ParseFrom(token string, r io.ReaderAt, size int64, ext string) contract.StageResult {
	s.pruneOrphans()
	parsed, err := parseMeta(r, size, ext)
	if err != nil {
		log.Printf("解析降级 token=%s ext=%s err=%v", token, ext, err)
		return parseResult(token, parsed, parseWarning(err))
	}
	return parseResult(token, parsed, "")
}

// parseMeta 是解析的唯一内核：把 ReaderAt + 扩展名交给 parser，不关心数据来自磁盘还是网络。
func parseMeta(r io.ReaderAt, size int64, ext string) (contract.ParsedMeta, error) {
	platform, ok := contract.PlatformOfExt(ext)
	if !ok {
		return contract.ParsedMeta{}, fmt.Errorf("未知平台扩展名: %s", ext)
	}
	return parser.Parse(r, size, platform)
}

// parseResult 统一把解析结果包装成 StageResult：有明确错误用其文案，字段缺失用通用提示。
func parseResult(token string, parsed contract.ParsedMeta, warning string) contract.StageResult {
	result := contract.StageResult{StageToken: token, Parsed: parsed, Warnings: []string{}}
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	} else if parsed.AppName == "" || parsed.PackageID == "" || parsed.Version == "" || parsed.BuildNumber == "" {
		result.Warnings = append(result.Warnings, "部分信息未自动识别，可手动修正")
	}
	return result
}

func parseWarning(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "超时"):
		return "解析超时，请手动填写应用名、包名与版本号"
	case strings.Contains(message, "hap"):
		return "暂不支持解析 .hap，请手动填写应用名、包名与版本号"
	case strings.Contains(message, "zip"), strings.Contains(message, "plist"), strings.Contains(message, "清单"):
		return "安装包结构无法识别，请手动填写应用名、包名与版本号"
	default:
		return "部分信息未自动识别，可手动修正"
	}
}

// validateStageToken 校验 UUID 格式、tmp 前缀、文件存在与 1 小时有效期。
// 通过文件名前缀查找，是为了在平台与暂存文件扩展名不一致时给出精确的中文提示。
func (s *BuildService) validateStageToken(token string) (stagedLookup, error) {
	if !contract.IsValidUUID(token) {
		return stagedLookup{}, contract.ErrStageInvalid
	}
	entries, err := os.ReadDir(s.paths.TmpDir())
	if err != nil {
		return stagedLookup{}, contract.ErrStageInvalid
	}
	namePath := s.paths.StageNamePath(token)
	var found string
	var ext string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 跳过两个伴生文件：.name 是原始文件名，.r2 记录直传对象名。
		if !strings.HasPrefix(name, token+".") || name == token+".name" || name == token+".r2" {
			continue
		}
		candidate := strings.TrimPrefix(name, token+".")
		// 用契约的扩展名映射判断，而不是写死 apk/ipa：新增平台（如鸿蒙 .hap）不用改这里。
		if _, ok := contract.PlatformOfExt(candidate); ok {
			found = name
			ext = candidate
			break
		}
	}
	if found == "" {
		return stagedLookup{}, contract.ErrStageInvalid
	}

	abs, err := s.paths.SafeResolveStage(filepath.Join(s.paths.TmpDir(), found))
	if err != nil {
		return stagedLookup{}, contract.ErrStageInvalid
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return stagedLookup{}, contract.ErrStageInvalid
	}
	if time.Since(info.ModTime()) > time.Duration(contract.StageTokenTTLSeconds)*time.Second {
		return stagedLookup{}, contract.ErrStageInvalid
	}

	// .name 伴生文件保存 stage 时清洗后的原始文件名，供下载名与展示使用。
	original := "package." + ext
	if data, err := os.ReadFile(namePath); err == nil {
		clean := storage.CleanFileName(string(data))
		if clean != "" {
			original = clean
		}
	}
	// .r2 伴生文件只在浏览器直传时存在：记录已在桶里的对象名。
	r2Key := ""
	if data, err := os.ReadFile(s.paths.StageR2KeyPath(token)); err == nil {
		r2Key = strings.TrimSpace(string(data))
	}
	return stagedLookup{absPath: abs, namePath: namePath, ext: ext, originalFileName: original, r2Key: r2Key}, nil
}

// Create 是提交的唯一编排：表单即最终真值，提交时不再解析、不再覆盖用户值。
// Create 发布一个包；identity 决定写入哪个分区（单人写 solo，团队与外部 API 写 team），
// 它的 UserID 同时作为包的 owner，供单人分区按账号隔离。
func (s *BuildService) Create(ctx context.Context, in contract.CommitInput, identity contract.Identity) (contract.Build, error) {
	mode := identity.Mode
	repo := s.repos.For(mode)
	project, err := s.resolveProject(ctx, in.Project, identity.UserID, mode)
	if err != nil {
		return contract.Build{}, err
	}
	staged, err := s.validateStageToken(in.StageToken)
	if err != nil {
		return contract.Build{}, err
	}
	ext, err := storage.FileExt(in.Platform)
	if err != nil || ext != staged.ext {
		return contract.Build{}, contract.ErrPlatformMismatch
	}

	// 文件落盘必须先有 id，故先向 PG 申请一个 UUID，写库时复用同一个 id。
	id, err := repo.NextID(ctx)
	if err != nil {
		return contract.Build{}, err
	}

	// 直传包只存在于 R2：不落本地、不下载整包，发布只需写库记录 + r2_key。
	// 文件大小从 R2 元数据读取（一次轻量 HEAD，非整包传输）。
	var fileSize int64
	if staged.r2Key != "" {
		size, exists, err := s.r2.StatObject(ctx, staged.r2Key)
		if err != nil || !exists {
			return contract.Build{}, fmt.Errorf("对象存储中找不到包文件: %w", err)
		}
		fileSize = size
		// 清理暂存标记，直传包无需本地文件。
		_ = os.Remove(staged.absPath)
		_ = os.Remove(staged.namePath)
		_ = os.Remove(s.paths.StageR2KeyPath(in.StageToken))
	} else {
		// 普通上传：rename 暂存文件到正式目录，作为权威副本。
		finalPath := s.paths.FinalPath(in.Environment, in.Platform, id, ext)
		if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
			return contract.Build{}, fmt.Errorf("创建正式目录失败: %w", err)
		}
		if err := os.Rename(staged.absPath, finalPath); err != nil {
			return contract.Build{}, fmt.Errorf("移动包文件失败: %w", err)
		}
		_ = os.Remove(staged.namePath)
		info, err := os.Stat(finalPath)
		if err != nil {
			return contract.Build{}, fmt.Errorf("读取包文件大小失败: %w", err)
		}
		fileSize = info.Size()
	}

	// 仅对空字段兜底；任何非空表单值（含用户手工修正值）都原样入库。
	appName := strings.TrimSpace(in.AppName)
	if appName == "" {
		appName = staged.originalFileName
	}
	packageID := strings.TrimSpace(in.PackageID)
	if packageID == "" {
		packageID = appName
	}
	version := strings.TrimSpace(in.Version)
	if version == "" {
		version = "-"
	}
	buildNumber := strings.TrimSpace(in.BuildNumber)
	if buildNumber == "" {
		buildNumber = "-"
	}
	// metaSource 由服务端派生，不进请求契约，前端无法伪造：
	// 只要发生过任一空值兜底就记为 fallback，否则说明表单值本身完整可用，记为 parsed。
	metaSource := contract.MetaParsed
	if strings.TrimSpace(in.AppName) == "" || strings.TrimSpace(in.PackageID) == "" ||
		strings.TrimSpace(in.Version) == "" || strings.TrimSpace(in.BuildNumber) == "" {
		metaSource = contract.MetaFallback
	}

	build := contract.Build{
		ID:               id,
		Environment:      in.Environment,
		Platform:         in.Platform,
		AppName:          appName,
		ProjectIconURL:   project.IconURL,
		PackageID:        packageID,
		Version:          version,
		BuildNumber:      buildNumber,
		Changelog:        strings.TrimSpace(in.Changelog),
		OriginalFileName: staged.originalFileName,
		FileSize:         fileSize,
		FileExt:          ext,
		RelativePath:     s.paths.RelativePath(in.Environment, in.Platform, id, ext),
		MetaSource:       metaSource,
		DownloadCount:    0,
		OwnerUserID:      identity.UserID,
	}

	if err := repo.Insert(ctx, project.ID, &build); err != nil {
		// 失败补偿：普通上传已把文件 move 到正式目录，写库失败要删除它避免孤儿包。
		// 直传包没有本地文件，无需补偿（R2 对象由后续过期清理处理）。
		if staged.r2Key == "" {
			orphan := s.paths.FinalPath(in.Environment, in.Platform, id, ext)
			if removeErr := os.Remove(orphan); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				log.Printf("失败补偿删除正式文件失败 path=%s err=%v", orphan, removeErr)
			}
		}
		return contract.Build{}, err
	}

	items := []contract.Build{build}
	if err := s.fillShareURLs(ctx, items, mode); err != nil {
		return contract.Build{}, err
	}
	build = items[0]
	// ★ 唯一「上传完成」时点：文件已落盘 + DB 已提交 + 绝对地址已生成且可访问。
	if s.notifier != nil {
		// 旁路通知：实现方异步投递，这里不等待、不重试，发布结果不受影响。
		// 公司归属取自项目（项目是包的唯一归属载体），不是请求参数。
		s.notifier.NotifyBuild(ctx, project.TenantID, build)
	}
	// 直传来源的对象已在桶里：直接回写 r2_key，省掉一次整包上传。
	if staged.r2Key != "" && s.r2 != nil {
		if err := repo.SetR2Key(ctx, build.ID, staged.r2Key); err != nil {
			log.Printf("直传包回写 r2_key 失败 id=%s key=%s err=%v", build.ID, staged.r2Key, err)
			// 回写失败不阻断：交给后台镜像兜底。
			s.syncToR2Async(build, mode)
		} else {
			build.R2Key = staged.r2Key
			// 预热 CDN：让首个下载者命中边缘缓存，而不是等待回源。
			s.prewarmCDNAsync(staged.r2Key)
		}
		return build, nil
	}
	// 镜像到 R2 是纯加速旁路：后台执行，失败只记日志。
	// 首次下载若还没传完，会自动回落到本机流式，用户无感。
	s.syncToR2Async(build, mode)
	return build, nil
}

// buildURLs 是唯一 URL 生成处，仅使用 PUBLIC_BASE_URL；内部预览路径不含凭证。
func (s *BuildService) buildURLs(id, token string) (pagePath, pageURL, fileURL string) {
	pagePath = strings.Replace(contract.RouteDownloadPage, "{id}", id, 1)
	if token == "" {
		return pagePath, "", ""
	}
	query := "?" + url.Values{"shareToken": {token}}.Encode()
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	fileRel := strings.Replace(contract.RouteSharedBuildFile, "{id}", id, 1)
	return pagePath, base + pagePath + query, base + fileRel + query
}

func (s *BuildService) fillURLs(b *contract.Build, token string) {
	b.DownloadPagePath, b.DownloadPageURL, b.DownloadFileURL = s.buildURLs(b.ID, token)
}

// List 返回该项目的按时间倒序列表，空结果为空数组；统一填充三个 URL 字段。
// 分区由 identity 决定：单人只可能看到自己分区里的包，团队看到团队分区。
// owner 一并下传：单人分区的仓储会把它变成 SQL 条件，隔离不只依赖这次 resolveProject。
func (s *BuildService) List(ctx context.Context, q contract.ListQuery, identity contract.Identity) (contract.ListResult, error) {
	project, err := s.resolveProject(ctx, q.Project, identity.UserID, identity.Mode)
	if err != nil {
		return contract.ListResult{}, err
	}
	items, err := s.repos.For(identity.Mode).List(ctx, project.ID, q, identity.UserID)
	if err != nil {
		return contract.ListResult{}, err
	}
	if err := s.fillShareURLs(ctx, items, identity.Mode); err != nil {
		return contract.ListResult{}, err
	}
	platform := q.Platform
	if platform == "" {
		platform = "all"
	}
	environment := q.Environment
	if environment == "" {
		environment = "all"
	}
	return contract.ListResult{
		Items:       items,
		Total:       len(items),
		Environment: environment,
		Platform:    platform,
	}, nil
}

// resolveBuildFile 只接收已经授权的包，统一做路径与文件存在性检查。
//
// 直传包的字节只在 R2、本地无文件：此时跳过磁盘检查，Size 用数据库记录。
// streamFile 见 R2Key 非空会 302 到 CDN，不读 AbsPath；仅当 R2 不可用时才回退本地，
// 那种情况下 AbsPath 为空会安全地返回 NotFound，不会误下发错误内容。
func (s *BuildService) resolveBuildFile(build contract.Build) (contract.FileRef, error) {
	id := build.ID
	abs, err := s.paths.SafeResolve(build.RelativePath)
	if err != nil {
		log.Printf("下载路径校验失败 id=%s rel=%s err=%v", id, build.RelativePath, err)
		return contract.FileRef{}, contract.NotFound(contract.ResourceBuild)
	}

	size := build.FileSize
	if build.R2Key == "" {
		// 普通上传：本地文件是权威副本，必须存在。
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			log.Printf("下载文件缺失 id=%s path=%s err=%v", id, abs, err)
			return contract.FileRef{}, contract.NotFound(contract.ResourceBuild)
		}
		size = info.Size()
	}

	contentType, err := storage.ContentType(build.Platform)
	if err != nil {
		return contract.FileRef{}, err
	}
	fileName := build.OriginalFileName
	if !strings.HasSuffix(strings.ToLower(fileName), "."+build.FileExt) {
		fileName = fileName + "." + build.FileExt
	}
	return contract.FileRef{
		AbsPath:     abs,
		ContentType: contentType,
		FileName:    fileName,
		Size:        size,
		R2Key:       build.R2Key,
	}, nil
}

// IncrDownloadCount 只在已授权且完整流式传输后调用，分区沿用本次授权结果。
func (s *BuildService) IncrDownloadCount(id string, mode contract.SessionMode) {
	if !mode.IsValid() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.repos.For(mode).IncrDownloadCount(ctx, id); err != nil {
		log.Printf("下载计数旁路失败（不影响本次下载）id=%s err=%v", id, err)
	}
}
