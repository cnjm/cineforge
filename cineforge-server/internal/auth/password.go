package auth

// 密码哈希契约（对齐 legacy FastAPI app/security.py）：
//
//   - 存储格式：pbkdf2_sha256${salt}${hex}
//   - 迭代 120,000 次，HMAC-SHA256，输出取 hex 字符串
//   - salt 为 16 字节随机 → 32 个 hex 字符；pbkdf2 的 salt 参数用
//     这些 ASCII 字符的 UTF-8 bytes（与 Python salt.encode("utf-8") 一致）
//   - 验证用 constant-time 比较，scheme 不匹配/格式错误一律 false

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	pbkdf2Scheme  = "pbkdf2_sha256"
	pbkdf2Rounds  = 120000
	pbkdf2KeyLen  = 32
	pbkdf2SaltLen = 16 // bytes → 32 hex chars
)

// ErrPhoneTooShort 是 defaultPasswordForPhone 的输入保护（不应通过正常调用触发）。
var ErrPhoneTooShort = errors.New("phone too short for default password")

// HashPassword 生成 pbkdf2_sha256$salt$hex。等价 Python hash_password。
func HashPassword(password string) (string, error) {
	saltBytes := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(saltBytes); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	salt := hex.EncodeToString(saltBytes)
	dk := pbkdf2.Key([]byte(password), []byte(salt), pbkdf2Rounds, pbkdf2KeyLen, sha256.New)
	return strings.Join([]string{pbkdf2Scheme, salt, hex.EncodeToString(dk)}, "$"), nil
}

// VerifyPassword 校验明文是否匹配存储 hash。hash 为空/非法格式返回 false。
func VerifyPassword(hash, password string) bool {
	if hash == "" {
		return false
	}
	parts := strings.SplitN(hash, "$", 3)
	if len(parts) != 3 || parts[0] != pbkdf2Scheme {
		return false
	}
	salt, expected := parts[1], parts[2]
	dk := pbkdf2.Key([]byte(password), []byte(salt), pbkdf2Rounds, pbkdf2KeyLen, sha256.New)
	got := hex.EncodeToString(dk)
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

// DefaultPasswordForPhone 返回手机号后 6 位（未提供新密码时的默认密码）。
func DefaultPasswordForPhone(phone string) string {
	if len(phone) <= 6 {
		return phone
	}
	return phone[len(phone)-6:]
}
