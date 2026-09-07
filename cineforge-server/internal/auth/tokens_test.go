package auth

// auth 包白盒测试：PBKDF2 与 token 算法。
// 含与 legacy Python 实现的互操作向量（详见各测试注释）。

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"cineforge/server/internal/config"
)

// ---- PBKDF2 ----

// 与 Python hash_password 的互操作向量（salt=32 个 '0'，password=123456）。
const pythonPBKDF2Vector = "pbkdf2_sha256$00000000000000000000000000000000$2f1aa36930c52eeedb447b7a2c06b68a6e6251bfbf9ea011ba2853be90a1b145"

func TestVerifyPasswordPythonVector(t *testing.T) {
	if !VerifyPassword(pythonPBKDF2Vector, "123456") {
		t.Fatal("Go PBKDF2 应当验证通过 Python 生成的 hash")
	}
	if VerifyPassword(pythonPBKDF2Vector, "654321") {
		t.Fatal("错误密码不应通过")
	}
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
	if VerifyPassword("", "x") {
		t.Fatal("空 hash 应为 false")
	}
	if VerifyPassword("argon2$foo$bar", "x") {
		t.Fatal("未知 scheme 应为 false")
	}
	if VerifyPassword("pbkdf2_sha256$salt_only", "x") {
		t.Fatal("不足三段应为 false")
	}
}

func TestHashPasswordRoundtrip(t *testing.T) {
	h, err := HashPassword("  password-测试  ")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !VerifyPassword(h, "  password-测试  ") {
		t.Fatal("roundtrip 校验应通过")
	}
	if VerifyPassword(h, "  password-測試  ") {
		t.Fatal("不同明文不应通过")
	}
}

func TestDefaultPasswordForPhone(t *testing.T) {
	if got := DefaultPasswordForPhone("13800138000"); got != "138000" {
		t.Fatalf("phone 后 6 位: got %q", got)
	}
	if got := DefaultPasswordForPhone("short"); got != "short" {
		t.Fatalf("短 phone 原样返回: got %q", got)
	}
}

// ---- access_token（2-part 私有格式）----

// 与 Python security.py 的互操作向量（secret="sekret"）。
const (
	pythonAccessBody = "eyJzdWIiOiIwMDAwMDAwMC0wMDAwLTAwMDAtMDAwMC0wMDAwMDAwMDAwMDAiLCJyb2xlIjoiZGlyZWN0b3IiLCJleHAiOjIwMDAwMDAwMDB9"
	pythonAccessTok  = pythonAccessBody + ".jM9f8ldLF5V7geIrJM2RJJAZ1SVBYlkJgWY7AnWpTP4"
)

func TestVerifyPythonAccessTokenVector(t *testing.T) {
	c, err := VerifyAccessToken("sekret", pythonAccessTok)
	if err != nil {
		t.Fatalf("verify python access token: %v", err)
	}
	if c.Sub != "00000000-0000-0000-0000-000000000000" || c.Role != "director" || c.Exp != 2000000000 {
		t.Fatalf("claims mismatch: %+v", c)
	}
}

func TestSignAccessTokenRoundtrip(t *testing.T) {
	secret := "test-secret"
	tok := SignAccessToken(secret, "u-1", "editor", 2000000000)
	c, err := VerifyAccessToken(secret, tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.Sub != "u-1" || c.Role != "editor" || c.Exp != 2000000000 {
		t.Fatalf("claims mismatch: %+v", c)
	}
}

func TestAccessTokenTamperRejected(t *testing.T) {
	secret := "test-secret"
	tok := SignAccessToken(secret, "u-1", "editor", time.Now().Add(time.Hour).Unix())
	if _, err := VerifyAccessToken("other-secret", tok); err == nil {
		t.Fatal("错误密钥应拒绝")
	}
	tampered := "AAAA" + tok[4:]
	if _, err := VerifyAccessToken(secret, tampered); err == nil {
		t.Fatal("篡改 body 应拒绝")
	}
}

func TestAccessTokenExpired(t *testing.T) {
	tok := SignAccessToken("s", "u", "artist", time.Now().Add(-time.Minute).Unix())
	if _, err := VerifyAccessToken("s", tok); err != ErrTokenExpired {
		t.Fatalf("期望 ErrTokenExpired，got %v", err)
	}
}

// ---- tapcanvas_token（HS256）----

func TestSignTapcanvasToken(t *testing.T) {
	i, e := int64(1000), int64(2000)
	tok, err := SignTapcanvasToken("tapsecret", "u-9", "ali", "阿狸", "director", i, e)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	header, claims := parseJWS(t, tok)
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Fatalf("header: %+v", header)
	}
	if claims["sub"] != "cineforge:u-9" || claims["login"] != "ali" || claims["name"] != "阿狸" {
		t.Fatalf("claims: %+v", claims)
	}
	if claims["role"] != "platformAdmin" {
		t.Fatalf("director 映射 platformAdmin，got %v", claims["role"])
	}
	if int64(claims["exp"].(float64)) != e || int64(claims["iat"].(float64)) != i {
		t.Fatalf("iat/exp: %+v", claims)
	}
}

func TestTapcanvasRoleMapping(t *testing.T) {
	cases := map[string]string{"admin": "platformAdmin", "director": "platformAdmin",
		"editor": "editor", "script_editor": "editor", "artist": "member"}
	for role, want := range cases {
		if got := tapcanvasRole(role); got != want {
			t.Fatalf("role %s → %s, want %s", role, got, want)
		}
	}
}

func TestSignTapcanvasTokenRequiresSecret(t *testing.T) {
	if _, err := SignTapcanvasToken("", "u", "n", "n", "editor", 1, 2); err == nil {
		t.Fatal("空 secret 应报错")
	}
}

// ---- platform_token（RS256）----

// testRSAPEM 用运行时生成的一次性 RSA-2048 PKCS1 PEM（避免硬编码密钥拼写错误）。
func testRSAPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func testAuthCfg(t *testing.T) *config.AuthConfig {
	t.Helper()
	return &config.AuthConfig{
		Secret:                "test-secret",
		SessionTTLSeconds:     28800,
		TapcanvasJWTSecret:    "test-tap-secret",
		PlatformJWTPrivateKey: testRSAPEM(t),
		PlatformJWTKID:        "test-kid",
	}
}

func TestSignPlatformTokenClaims(t *testing.T) {
	cfg := testAuthCfg(t)
	tok, err := SignPlatformToken(cfg, "u-7", 1000, 2000)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	header, claims := parseJWS(t, tok)
	if header["alg"] != "RS256" || header["typ"] != "JWT" || header["kid"] != "test-kid" {
		t.Fatalf("header: %+v", header)
	}
	if claims["iss"] != "cine-forge-platform" || claims["aud"] != "cine-forge-go" {
		t.Fatalf("iss/aud: %+v", claims)
	}
	if claims["sub"] != "cineforge:u-7" {
		t.Fatalf("sub: %v", claims["sub"])
	}
	if claims["tenant_id"] != "default" {
		t.Fatalf("tenant_id: %v", claims["tenant_id"])
	}
	if int64(claims["exp"].(float64)) != 2000 || int64(claims["iat"].(float64)) != 1000 {
		t.Fatalf("iat/exp: %+v", claims)
	}
	if jti, _ := claims["jti"].(string); jti == "" {
		t.Fatal("jti 应非空")
	}
}

func TestSignPlatformTokenRequiresKey(t *testing.T) {
	empty := &config.AuthConfig{PlatformJWTPrivateKey: "", PlatformJWTKID: "k"}
	if _, err := SignPlatformToken(empty, "u", 1, 2); err == nil {
		t.Fatal("空私钥应报错")
	}
}

// parseJWS 解析三段式 JWS 的 header/payload。
func parseJWS(t *testing.T, tok string) (map[string]any, map[string]any) {
	t.Helper()
	seg := strings.Split(tok, ".")
	if len(seg) != 3 {
		t.Fatalf("非三段 JWS: %d 段", len(seg))
	}
	var header, claims map[string]any
	hb, err1 := base64.RawURLEncoding.DecodeString(seg[0])
	cb, err2 := base64.RawURLEncoding.DecodeString(seg[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("b64 decode: %v %v", err1, err2)
	}
	if err := json.Unmarshal(hb, &header); err != nil {
		t.Fatalf("header unmarshal: %v", err)
	}
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("claims unmarshal: %v", err)
	}
	return header, claims
}
