// Package modelkey 是全仓唯一接触模型 API Key 明文的包。
// 职责：用 AES-256-GCM 加密 / 解密 Key，并提供唯一掩码与脱敏函数。
// 输入：config 读入的 32 字节主密钥、待入库的 Key 明文、服务端内存中的 Key 与待摘要文本。
// 输出：base64 密文与 nonce、解密后的明文、掩码串、脱敏文本。
// 边界：不读环境变量、不访问 DB / 网络 / 文件系统；不记录明文，错误不回显密文或原文。
package modelkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// MasterKeyBytes 是 AES-256 主密钥的精确长度；缺失或长度不符由 config 在启动阶段 fail-fast。
	MasterKeyBytes = 32
	nonceBytes     = 12
)

var (
	errInvalidMasterKey = errors.New("主密钥必须是 base64 编码的 32 字节")
	errInvalidCipher    = errors.New("密钥密文格式非法")
	// secretPattern 兜住常见的 sk- 样式 Key，即便调用方忘记把明文传给 Redact 也不会原样上屏。
	secretPattern = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
)

// Codec 持有主密钥的拷贝，只提供 Seal / Open 两种加解密能力。
type Codec struct {
	key [MasterKeyBytes]byte
}

// New 校验并拷贝主密钥；调用方传入的切片随后被修改也不会影响本 Codec。
func New(masterKey []byte) (*Codec, error) {
	if len(masterKey) != MasterKeyBytes {
		return nil, errInvalidMasterKey
	}
	codec := &Codec{}
	copy(codec.key[:], masterKey)
	return codec, nil
}

// Seal 用随机 12 字节 nonce 做 AES-GCM 加密，返回 base64(密文+tag) 与 base64(nonce)。
func (c *Codec) Seal(plain string) (string, string, error) {
	if c == nil {
		return "", "", errInvalidMasterKey
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", "", fmt.Errorf("初始化 AES 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", "", fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	nonce := make([]byte, nonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", fmt.Errorf("生成随机数失败: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(sealed), base64.StdEncoding.EncodeToString(nonce), nil
}

// Open 只在内存中解密；调用方用完即弃，禁止把返回值写入 DTO、日志或响应。
func (c *Codec) Open(ciphertextB64, nonceB64 string) (string, error) {
	if c == nil {
		return "", errInvalidMasterKey
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil || len(ciphertext) == 0 {
		return "", errInvalidCipher
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil || len(nonce) != nonceBytes {
		return "", errInvalidCipher
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", fmt.Errorf("初始化 AES 失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// 绝不回显密文、nonce 或 GCM 的具体失败原因。
		return "", errInvalidCipher
	}
	return string(plain), nil
}

// Mask 是唯一掩码函数：保留最后一个 '-' 及其前缀 + **** + 末 4 位；
// 长度不足或没有足够末 4 位时整串打码，绝不输出可用于推断的片段。
func Mask(plain string) string {
	if len(plain) <= 8 {
		return "****"
	}
	prefix := ""
	if hyphen := strings.LastIndex(plain, "-"); hyphen >= 0 && hyphen < len(plain)-5 {
		prefix = plain[:hyphen+1]
	}
	suffix := plain[len(plain)-4:]
	if strings.TrimSpace(suffix) == "" {
		return "****"
	}
	return prefix + "****" + suffix
}

// Redact 把文本中的明文 Key 与常见密钥样式替换为 [已隐藏]，供实测摘要使用。
func Redact(plain, text string) string {
	result := text
	if plain != "" {
		result = strings.ReplaceAll(result, plain, "[已隐藏]")
	}
	return secretPattern.ReplaceAllString(result, "[已隐藏]")
}
