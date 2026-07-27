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

func (h *PasswordHasher) HashPassword(raw string) string {
	saltBytes := make([]byte, 16)
	_, _ = rand.Read(saltBytes)
	salt := hex.EncodeToString(saltBytes)
	digest := hex.EncodeToString(pbkdf2.Key([]byte(raw), []byte(salt), h.iterations, 32, sha256.New))
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", h.iterations, salt, digest)
}

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
