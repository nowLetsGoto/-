// Package secret · 不可逆外存凭据的对称加密。
// 职责：把需要还原明文的凭据（如 R2 Secret Key）加密后落库，读时解密。
// 输入：主密钥（来自配置的 AUTH_SECRET）与调用方给出的明文。
// 输出：带版本前缀的密文串，或解密后的明文。
// 边界：不校验业务语义、不管理密钥轮换、不接触数据库；只做「明文 ↔ 密文」。
//
// 为什么单独成包：这是唯一需要「可逆」的存储场景。system_settings 里的
// api_key 只需比对，存 SHA-256 摘要即可；而 R2 签名必须拿到原文，
// 摘要无能为力，只能用对称加密。两种需求方向相反，混在一处必然写出含糊的代码。
//
// 密钥为什么从 AUTH_SECRET 派生而不是新增一个环境变量：
// 部署方已经必须配置 AUTH_SECRET（缺它服务起不来），复用它省掉一个必填项；
// 用 HKDF 按用途派生独立子密钥，使登录令牌签名与凭据加密使用不同的密钥流，
// 一方泄露不会牵连另一方。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// versionPrefix 标记密文格式版本，形如 "v1:"。
//
// 不是装饰：将来若换算法或换派生参数，靠它识别旧数据并决定走哪条解密路径。
// 没有版本号的密文一旦算法变更就无法平滑迁移——只能要求所有人重填凭据。
const versionPrefix = "v1:"

// hkdfInfo 是派生凭据密钥时的用途标签。
// 换掉它会改变派生结果，等于让所有存量密文失效，因此不可随意修改。
const hkdfInfo = "dandelion/credential-encryption"

// Cipher 用固定主密钥加解密凭据，进程内复用一份。
//
// 并发安全：底层 cipher.AEAD 无内部可变状态，可被多个 goroutine 同时调用。
type Cipher struct {
	aead cipher.AEAD
}

// New 从主密钥派生加密密钥。
//
// 参数：master 是 AUTH_SECRET 的原文（不得为空）。
// 返回：可用于加解密的 Cipher。
//
// 派生过程分两步而不是直接拿 master 当 AES 密钥：
// AUTH_SECRET 是人配的字符串，长度不定（可能是 20 字节也可能是 64），
// 而 AES 只接受 16/24/32 字节；HKDF 把它规整成固定 32 字节，
// 同时通过 info 标签隔离用途。
func New(master string) (*Cipher, error) {
	if master == "" {
		return nil, errors.New("主密钥不能为空")
	}

	// SHA-256 先把任意长度的 master 压成固定长度，作为 HKDF 的输入密钥材料。
	// 这一步不是加密，只是让输入长度稳定，避免超长 master 影响派生性能。
	sum := sha256.Sum256([]byte(master))
	key, err := hkdf.Key(sha256.New, sum[:], nil, hkdfInfo, 32)
	if err != nil {
		return nil, fmt.Errorf("派生凭据密钥失败: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化 GCM 失败: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密明文，返回带版本前缀的 base64 串。
//
// 空明文返回空串而不是密文：调用方用空串表达「未配置」，
// 若把空串加密成一段非空密文，"是否配置过" 就得多一个字段来判断。
func (c *Cipher) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}

	// 每次加密都用新的随机 nonce。GCM 的安全性要求 nonce 不复用，
	// 同一密钥下重复 nonce 会直接泄露明文异或值。
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成随机数失败: %w", err)
	}

	// nonce 前置拼在密文前：解密时需要它，但它不必保密。
	// 这样密文是自包含的，不需要额外存一列。
	sealed := c.aead.Seal(nonce, nonce, []byte(plain), nil)
	return versionPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// Decrypt 还原 Encrypt 的结果。
//
// 失败一律返回错误而不返回部分明文：GCM 的校验失败意味着密文被篡改或密钥不对，
// 此时任何"尽力解出"的结果都不可信。
func (c *Cipher) Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if !strings.HasPrefix(stored, versionPrefix) {
		return "", fmt.Errorf("凭据格式无法识别（缺少 %q 前缀）", versionPrefix)
	}

	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, versionPrefix))
	if err != nil {
		return "", fmt.Errorf("凭据不是合法的 base64: %w", err)
	}

	size := c.aead.NonceSize()
	if len(raw) < size {
		return "", errors.New("凭据长度不足，可能已损坏")
	}
	plain, err := c.aead.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		// 最常见的原因是 AUTH_SECRET 变更导致派生出的密钥不同。
		// 这里不直接断言原因，由调用方决定如何提示用户。
		return "", errors.New("凭据解密失败：主密钥可能已变更")
	}
	return string(plain), nil
}
