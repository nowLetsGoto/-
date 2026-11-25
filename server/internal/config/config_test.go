// config_test.go · AI 模型主密钥的 fail-fast 校验。
// 职责：验证 RDC_MODEL_ENCRYPTION_KEY 缺失 / 非法 base64 / 长度错误时 Load 返回错误。
// 输入：测试进程环境变量（由 t.Setenv 提供，均为测试专用值，不是任何真实密钥）。
// 输出：合法配置通过，非法配置被拒绝；被测错误文案不含环境变量内容。
// 边界：不启动服务、不访问数据库；每个用例结束由 t.Setenv 自动恢复环境。
package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

const testMasterKey = "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

func prepareEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GO_ENV", "development")
	t.Setenv("DATABASE_URL", "postgres://dandelion:dandelion@localhost:5432/dandelion?sslmode=disable")
	t.Setenv("RDC_GIT_ROOTS", "")
}

func TestModelEncryptionKeyRequired(t *testing.T) {
	prepareEnv(t)
	t.Setenv("RDC_MODEL_ENCRYPTION_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("缺少 RDC_MODEL_ENCRYPTION_KEY 必须 fail-fast")
	}
}

func TestModelEncryptionKeyMustBeBase64Of32Bytes(t *testing.T) {
	prepareEnv(t)
	for _, invalid := range []string{"not-base64!!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		t.Setenv("RDC_MODEL_ENCRYPTION_KEY", invalid)
		_, err := Load()
		if err == nil {
			t.Fatalf("非法主密钥 %q 必须 fail-fast", invalid)
		}
		if strings.Contains(err.Error(), invalid) {
			t.Fatal("错误文案不得回显主密钥内容")
		}
	}
}

func TestModelEncryptionKeyValid(t *testing.T) {
	prepareEnv(t)
	t.Setenv("RDC_MODEL_ENCRYPTION_KEY", testMasterKey)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("合法 32 字节主密钥应通过: %v", err)
	}
	if len(cfg.ModelEncryptionKey) != 32 {
		t.Fatalf("主密钥长度应为 32，实际 %d", len(cfg.ModelEncryptionKey))
	}
}
