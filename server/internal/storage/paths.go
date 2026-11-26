// Package storage 统一生成、清洗与校验安装包的存储路径。
// 职责：区分 tmp 暂存与 uploads 正式目录，阻止路径穿越，清洗展示文件名。
// 输入：存储根目录、环境、平台、包 ID、暂存令牌、扩展名与待解析路径。
// 输出：规范化的绝对路径、相对 uploads 的路径、平台文件类型或校验错误。
// 边界：仅做词法路径运算，不访问文件系统；目录创建、文件读写与存在性检查交调用层。
package storage

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"dandelion/server/internal/contract"
)

var errInvalidPath = errors.New("存储路径不合法")

// Paths 在初始化时固定绝对根目录，不随之后的工作目录变化。
type Paths struct {
	root string
}

func NewPaths(root string) (*Paths, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("存储根目录不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析存储根目录失败: %w", err)
	}
	return &Paths{root: abs}, nil
}

func (p *Paths) Root() string       { return p.root }
func (p *Paths) TmpDir() string     { return filepath.Join(p.root, "tmp") }
func (p *Paths) UploadsDir() string { return filepath.Join(p.root, "uploads") }

func (p *Paths) StagePath(token, ext string) string {
	return filepath.Join(p.TmpDir(), token+"."+ext)
}

func (p *Paths) StageNamePath(token string) string {
	return filepath.Join(p.TmpDir(), token+".name")
}

// StageR2KeyPath 是直传上传的伴生文件：记录该暂存文件在 R2 上的对象名。
// commit 读到它就跳过二次上传，直接把 build 的 r2_key 指向已有对象。
func (p *Paths) StageR2KeyPath(token string) string {
	return filepath.Join(p.TmpDir(), token+".r2")
}

// RelativePath 只含 uploads 内部层级，数据库路径统一使用正斜杠。
func (p *Paths) RelativePath(environment, platform, id, ext string) string {
	return path.Join(environment, platform, id+"."+ext)
}

func (p *Paths) FinalPath(environment, platform, id, ext string) string {
	return filepath.Join(p.UploadsDir(), filepath.FromSlash(p.RelativePath(environment, platform, id, ext)))
}

// SafeResolve 只接受 uploads 内的相对文件路径，禁止绝对路径与跨平台路径歧义。
func (p *Paths) SafeResolve(relative string) (string, error) {
	if !filepath.IsLocal(relative) || strings.ContainsAny(relative, "\\:\x00") {
		return "", errInvalidPath
	}
	return resolveWithin(p.UploadsDir(), filepath.Join(p.UploadsDir(), filepath.FromSlash(relative)))
}

// SafeResolveStage 接收服务层查得的绝对路径，必须严格位于 tmp 内。
func (p *Paths) SafeResolveStage(absolute string) (string, error) {
	if !filepath.IsAbs(absolute) || strings.ContainsRune(absolute, '\x00') {
		return "", errInvalidPath
	}
	return resolveWithin(p.TmpDir(), absolute)
}

func resolveWithin(base, candidate string) (string, error) {
	clean := filepath.Clean(candidate)
	relative, err := filepath.Rel(base, clean)
	if err != nil || relative == "." || !filepath.IsLocal(relative) {
		return "", errInvalidPath
	}
	return clean, nil
}

// CleanFileName 去除客户端目录与控制字符，保留中文和正常文件名字符。
func CleanFileName(raw string) string {
	name := path.Base(strings.ReplaceAll(raw, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	name = strings.TrimSpace(name)
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

// 文件类型只投影契约目录，避免新增平台时维护第二份映射。
func FileExt(platform string) (string, error) {
	info, ok := contract.PlatformInfos[platform]
	if !ok {
		return "", &contract.ValidationError{Message: "平台参数不合法"}
	}
	return info.Ext, nil
}

func ContentType(platform string) (string, error) {
	info, ok := contract.PlatformInfos[platform]
	if !ok {
		return "", &contract.ValidationError{Message: "平台参数不合法"}
	}
	return info.ContentType, nil
}
