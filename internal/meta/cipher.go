package meta

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Cipher 数据源密码加解密（AES-256-GCM）。
// 密钥来源：启动配置 meta.secret_key 或环境变量 BINCAT_SECRET_KEY（32 字节）。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 构造加解密器；key 长度必须 32 字节（base64 编码的 32 字节也可）
func NewCipher(key string) (*Cipher, error) {
	k, err := deriveKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("new aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func deriveKey(key string) ([]byte, error) {
	if key == "" {
		return nil, errors.New("secret key is empty (set meta.secret_key or BINCAT_SECRET_KEY)")
	}
	if len(key) == 32 {
		return []byte(key), nil
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, errors.New("secret key must be 32 bytes or base64 of 32 bytes")
}

// Encrypt 加密明文，输出 base64(nonce + ciphertext)
func (c *Cipher) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("gen nonce: %w", err)
	}
	ct := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	enc := make([]byte, base64.StdEncoding.EncodedLen(len(out)))
	base64.StdEncoding.Encode(enc, out)
	return enc, nil
}

// Decrypt 解密 Encrypt 的输出
func (c *Cipher) Decrypt(data []byte) (string, error) {
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(raw, data)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	raw = raw[:n]
	ns := c.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("ciphertext too short")
	}
	pt, err := c.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(pt), nil
}
