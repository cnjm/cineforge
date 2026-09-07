package auth

// 登录令牌实现（对齐 legacy FastAPI app/security.py + login_tokens.py + tapcanvas_sso.py + platform_sso.py）：
//
//   - access_token：私有 2-part {body}.{signature}，不是 JWT。
//     body = urlsafe_b64(compact json{"sub","role","exp"})，去掉尾部 =；
//     signature = urlsafe_b64(HMAC-SHA256(auth_secret, body))。
//   - tapcanvas_token：标准 HS256 JWT，sub="cineforge:<uuid>"，role 归一化。
//   - platform_token：标准 RS256 JWT，iss=cine-forge-platform、aud=cine-forge-go、
//     tenant_id=default、jti 随机。
//   - 三个 token exp 完全一致（同一会话）；TTL 必须 ∈ (0, 28800]（8h）。

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"cineforge/server/internal/config"
)

// MaxSessionSeconds 会话上限 8h；TTL 必须 ∈ (0, MaxSessionSeconds]。
const MaxSessionSeconds = 8 * 60 * 60

// 签名与校验错误（handler 统一映射 401，detail 见 API 层）。
var (
	ErrInvalidToken = errors.New("Invalid token")
	ErrTokenExpired = errors.New("Token expired")
)

// AccessClaims 是 access_token payload。
type AccessClaims struct {
	Sub  string `json:"sub"`
	Role string `json:"role"`
	Exp  int64  `json:"exp"`
}

// SignAccessToken 签发 2-part access_token。payload 字段序 sub,role,exp
// 与 Python json.dumps 插入序一致（紧凑无空格）。
func SignAccessToken(secret, userID, role string, exp int64) string {
	bodyJSON, _ := json.Marshal(AccessClaims{Sub: userID, Role: role, Exp: exp})
	body := base64.RawURLEncoding.EncodeToString(bodyJSON)
	return body + "." + b64urlHMAC(secret, body)
}

// VerifyAccessToken 验签并解析；过期返回 ErrTokenExpired，无效返回 ErrInvalidToken。
func VerifyAccessToken(secret, token string) (*AccessClaims, error) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidToken
	}
	body, sig := parts[0], parts[1]
	if err := verifyB64HMAC(secret, body, sig); err != nil {
		return nil, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c AccessClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, ErrInvalidToken
	}
	if c.Exp != 0 && c.Exp < time.Now().Unix() {
		return nil, ErrTokenExpired
	}
	return &c, nil
}

// TapcanvasClaims 是 tapcanvas_token payload。
type TapcanvasClaims struct {
	Sub   string `json:"sub"`
	Login string `json:"login"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	IAT   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

// tapcanvasRole 按 legacy tapcanvas_sso._map_role 映射。
func tapcanvasRole(role string) string {
	switch role {
	case "admin", "director":
		return "platformAdmin"
	case "editor", "script_editor":
		return "editor"
	default:
		return "member"
	}
}

// SignTapcanvasToken 签发 HS256 tapcanvas_token。
func SignTapcanvasToken(secret, userID, username, displayName, role string, iat, exp int64) (string, error) {
	if secret == "" {
		return "", errors.New("tapcanvas jwt secret not configured")
	}
	login := username
	if login == "" {
		short := userID
		if len(short) > 8 {
			short = short[:8]
		}
		login = "user_" + short
	}
	name := displayName
	if name == "" {
		name = username
	}
	claims := TapcanvasClaims{
		Sub:   "cineforge:" + userID,
		Login: login,
		Name:  name,
		Role:  tapcanvasRole(role),
		IAT:   iat,
		Exp:   exp,
	}
	return signJWS(secret, map[string]any{"alg": "HS256", "typ": "JWT"}, claims)
}

// PlatformClaims 是 platform_token payload。
type PlatformClaims struct {
	Iss      string `json:"iss"`
	Aud      string `json:"aud"`
	Sub      string `json:"sub"`
	TenantID string `json:"tenant_id"`
	IAT      int64  `json:"iat"`
	Exp      int64  `json:"exp"`
	JTI      string `json:"jti"`
}

// SignPlatformToken 签发 RS256 platform_token（PEM 私钥支持 \n 转义反转义）。
func SignPlatformToken(cfg *config.AuthConfig, userID string, iat, exp int64) (string, error) {
	if cfg.PlatformJWTPrivateKey == "" {
		return "", errors.New("platform jwt private key not configured")
	}
	if cfg.PlatformJWTKID == "" {
		return "", errors.New("platform jwt kid not configured")
	}
	key, err := parseRSAPrivateKey(cfg.PlatformJWTPrivateKey)
	if err != nil {
		return "", fmt.Errorf("parse platform private key: %w", err)
	}
	jti, err := NewUUID()
	if err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	claims := PlatformClaims{
		Iss:      "cine-forge-platform",
		Aud:      "cine-forge-go",
		Sub:      "cineforge:" + userID,
		TenantID: "default",
		IAT:      iat,
		Exp:      exp,
		JTI:      jti,
	}
	return signJWSMap(cfg.PlatformJWTKID, key, map[string]any{"alg": "RS256", "typ": "JWT"}, claims)
}

// ---- 底层 JWS 辅助 ----

// b64urlHMAC 计算 HMAC-SHA256(secret, msg) 并返回 urlsafe-b64 去填充。
func b64urlHMAC(secret, msg string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyB64HMAC(secret, msg, want string) error {
	got := b64urlHMAC(secret, msg)
	if !hmac.Equal([]byte(got), []byte(want)) {
		return ErrInvalidToken
	}
	return nil
}

// signJWS 签发 HMAC 签名的三段式 JWS（header.payload.signature）。
func signJWS(secret string, header map[string]any, claims any) (string, error) {
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	return body + "." + b64urlHMAC(secret, body), nil
}

// signJWSMap 签发 RSA 签名的三段式 JWS。
func signJWSMap(kid string, key *rsa.PrivateKey, header map[string]any, claims any) (string, error) {
	header["kid"] = kid
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	digest := sha256.Sum256([]byte(body))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return body + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// parseRSAPrivateKey 解析 PKCS8 / PKCS1 PEM 私钥。
func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	pemText = strings.ReplaceAll(pemText, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	if rk, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return rk, nil
	}
	return nil, errors.New("unsupported private key format")
}

// NewUUID 生成 v4 UUID 字符串（jti 与业务主键通用）。
func NewUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
