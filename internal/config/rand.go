package config

import "crypto/rand"

// randRead 与 hexEncode 抽出便于测试替身；M1 直接用 crypto/rand 与 encoding/hex
func randRead(b []byte) (int, error) { return rand.Read(b) }

func hexEncode(b []byte) string {
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}
