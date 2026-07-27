// Package security 密码哈希、签名 token 与敏感配置加密。
// 与原 Python 后端保持格式兼容:pbkdf2_sha256 密码、HS256 JWT、Fernet 密文。
package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fernet/fernet-go"
	"golang.org/x/crypto/pbkdf2"
)

// ErrInvalidToken token 校验失败。
var ErrInvalidToken = errors.New("invalid token")

// PasswordHasher PBKDF2-SHA256 密码哈希,格式 pbkdf2_sha256$iter$salt$hexdigest。
type PasswordHasher struct{ iterations int }

func NewPasswordHasher(iterations int) *PasswordHasher {
	if iterations <= 0 {
		iterations = 390000
	}
	return &PasswordHasher{iterations: iterations}
}

// HashPassword 把明文密码转成不可逆的哈希串入库:每次生成随机盐,再用 PBKDF2 迭代上万次。
// 目的是即便数据库泄露也难以暴力还原密码。输出格式与原 Python 版 pbkdf2_sha256 完全一致,老用户数据可直接沿用。
func (h *PasswordHasher) HashPassword(raw string) string {
	saltBytes := make([]byte, 16)
	_, _ = rand.Read(saltBytes)
	salt := hex.EncodeToString(saltBytes)
	digest := hex.EncodeToString(pbkdf2.Key([]byte(raw), []byte(salt), h.iterations, 32, sha256.New))
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", h.iterations, salt, digest)
}

// VerifyPassword 校验密码:从库里存的哈希串拆出盐和迭代次数,用同样参数把用户输入再算一遍,
// 再用 hmac.Equal 做"恒定时间比较"(无论对错都耗时相同,避免通过响应快慢猜密码)。
func (h *PasswordHasher) VerifyPassword(raw, hashed string) bool {
	parts := strings.SplitN(hashed, "$", 4)
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	derived := hex.EncodeToString(pbkdf2.Key([]byte(raw), []byte(parts[2]), iterations, 32, sha256.New))
	return hmac.Equal([]byte(derived), []byte(parts[3]))
}

// AuthToken 新签发的 token 及其元数据。
type AuthToken struct {
	Value     string
	TokenID   string
	ExpiresAt time.Time
}

// TokenPayload 校验通过后的载荷。
type TokenPayload struct {
	UserID    int64
	TokenType string
	TokenID   string
	ExpiresAt time.Time
}

// TokenManager HS256 签名 token(JWT 兼容格式)。
type TokenManager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewTokenManager(secretKey string, accessTTLMinutes, refreshTTLDays int) *TokenManager {
	return &TokenManager{
		secret:     []byte(secretKey),
		accessTTL:  time.Duration(accessTTLMinutes) * time.Minute,
		refreshTTL: time.Duration(refreshTTLDays) * 24 * time.Hour,
	}
}

func (m *TokenManager) CreateAccessToken(userID int64) AuthToken {
	return m.create(userID, "access", m.accessTTL)
}

func (m *TokenManager) CreateRefreshToken(userID int64) AuthToken {
	return m.create(userID, "refresh", m.refreshTTL)
}

// create 签发一个 JWT(登录令牌):由 header.payload.signature 三段组成,用点号连接,各段做 base64 编码。
// signature 是用服务端密钥对前两段做的 HS256 签名;别人改了内容但没有密钥,就算不出能对上的签名 → token 判为无效。
func (m *TokenManager) create(userID int64, tokenType string, ttl time.Duration) AuthToken {
	tokenID := randomHex(16)
	issuedAt := time.Now().Unix()
	expiresAt := issuedAt + int64(ttl.Seconds())
	header := b64JSON(map[string]any{"alg": "HS256", "typ": "JWT"})
	payload := b64JSON(map[string]any{
		"sub": userID,
		"typ": tokenType,
		"iat": issuedAt,
		"exp": expiresAt,
		"jti": tokenID,
	})
	signingInput := header + "." + payload
	signature := m.sign(signingInput)
	return AuthToken{
		Value:     signingInput + "." + signature,
		TokenID:   tokenID,
		ExpiresAt: time.Unix(expiresAt, 0),
	}
}

// VerifyToken 反向校验令牌:先用同一密钥重算签名比对(防篡改),再依次检查类型、是否过期、载荷是否合法。
func (m *TokenManager) VerifyToken(token, expectedType string) (*TokenPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: malformed", ErrInvalidToken)
	}
	expected := m.sign(parts[0] + "." + parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, fmt.Errorf("%w: bad signature", ErrInvalidToken)
	}
	rawPayload, err := b64Decode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: bad payload", ErrInvalidToken)
	}
	var payload struct {
		Sub int64  `json:"sub"`
		Typ string `json:"typ"`
		Exp int64  `json:"exp"`
		Jti string `json:"jti"`
	}
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		return nil, fmt.Errorf("%w: bad payload", ErrInvalidToken)
	}
	if payload.Typ != expectedType {
		return nil, fmt.Errorf("%w: unexpected type", ErrInvalidToken)
	}
	if payload.Exp <= time.Now().Unix() {
		return nil, fmt.Errorf("%w: expired", ErrInvalidToken)
	}
	if payload.Sub <= 0 || payload.Jti == "" {
		return nil, fmt.Errorf("%w: bad subject", ErrInvalidToken)
	}
	return &TokenPayload{
		UserID:    payload.Sub,
		TokenType: payload.Typ,
		TokenID:   payload.Jti,
		ExpiresAt: time.Unix(payload.Exp, 0),
	}, nil
}

// HashToken refresh token 入库前的 SHA256 摘要。
func HashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

// SecureCompare 恒定时间比较两个等长摘要/字符串,避免通过比较耗时的侧信道泄露信息(见评审 #9)。
func SecureCompare(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

func (m *TokenManager) sign(input string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// SecretCipher Fernet 对称加密,密钥由 secret_key 的 SHA256 派生。
type SecretCipher struct{ key *fernet.Key }

func NewSecretCipher(secretKey string) (*SecretCipher, error) {
	derived := sha256.Sum256([]byte(secretKey))
	key, err := fernet.DecodeKey(base64.URLEncoding.EncodeToString(derived[:]))
	if err != nil {
		return nil, err
	}
	return &SecretCipher{key: key}, nil
}

// Encrypt / Decrypt 用 Fernet 做对称加密:同一把密钥既能加密也能解密,
// 用来把 API Key、通知渠道密钥等敏感配置以密文形式存库。密钥由 secret_key 的 SHA256 派生,和 Python 版保持一致。
func (c *SecretCipher) Encrypt(plain string) string {
	normalized := strings.TrimSpace(plain)
	if normalized == "" {
		return ""
	}
	token, err := fernet.EncryptAndSign([]byte(normalized), c.key)
	if err != nil {
		return ""
	}
	return string(token)
}

func (c *SecretCipher) Decrypt(cipherText string) (string, error) {
	normalized := strings.TrimSpace(cipherText)
	if normalized == "" {
		return "", nil
	}
	plain := fernet.VerifyAndDecrypt([]byte(normalized), 0, []*fernet.Key{c.key})
	if plain == nil {
		return "", errors.New("敏感配置解密失败,请检查加密密钥是否一致")
	}
	return string(plain), nil
}

// Mask 前端展示用的脱敏字符串。
func Mask(secretText string) string {
	normalized := strings.TrimSpace(secretText)
	if normalized == "" {
		return ""
	}
	runes := []rune(normalized)
	if len(runes) <= 8 {
		return strings.Repeat("*", len(runes))
	}
	stars := len(runes) - 8
	if stars < 4 {
		stars = 4
	}
	return string(runes[:4]) + strings.Repeat("*", stars) + string(runes[len(runes)-4:])
}

// RandomToken 生成 32 位 hex 随机 token。
func RandomToken() string { return randomHex(16) }

// RandomURLSafe 生成 URL 安全随机串(近似 Python secrets.token_urlsafe)。
func RandomURLSafe(nbytes int) string {
	buf := make([]byte, nbytes)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// HashWebhookSecret webhook secret 加 pepper 后的单向哈希。
func HashWebhookSecret(pepper, secret string) string {
	digest := sha256.Sum256([]byte(pepper + ":" + secret))
	return hex.EncodeToString(digest[:])
}

func randomHex(nbytes int) string {
	buf := make([]byte, nbytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func b64JSON(value map[string]any) string {
	raw, _ := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func b64Decode(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}
