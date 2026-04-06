package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Claims struct {
	UserID    string
	Email     string
	Tier      string
	TokenType string
	ExpiresAt int64
}

func HashPassword(password string) (string, error) {
	raw, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func IssueToken(secret string, claims Claims, ttl time.Duration) (string, error) {
	claims.ExpiresAt = time.Now().Add(ttl).Unix()
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerRaw, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadRaw, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	headerPart := base64.RawURLEncoding.EncodeToString(headerRaw)
	payloadPart := base64.RawURLEncoding.EncodeToString(payloadRaw)
	signingInput := headerPart + "." + payloadPart
	signature := sign(secret, signingInput)
	return signingInput + "." + signature, nil
}

func ParseToken(secret, token string) (Claims, error) {
	var claims Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, errors.New("invalid token format")
	}
	signingInput := parts[0] + "." + parts[1]
	expected := sign(secret, signingInput)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return claims, errors.New("invalid token signature")
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, err
	}
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return claims, err
	}
	if claims.ExpiresAt > 0 && time.Now().Unix() > claims.ExpiresAt {
		return claims, errors.New("token expired")
	}
	return claims, nil
}

func sign(secret, input string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
