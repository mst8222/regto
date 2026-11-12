package authn

import "golang.org/x/crypto/bcrypt"

// dummyPasswordHash 是不对应任何明文的有效 bcrypt 哈希。
// 用途：账号不存在时也执行一次哈希比较，让「账号不存在」与「密码错误」的
// 响应时间落在同一量级，避免通过计时侧信道枚举账号（SPEC-AUTHN-001 NFR-03）。
var dummyPasswordHash = mustDummyHash()

// HashPassword 生成 bcrypt 哈希（默认 cost）。
func HashPassword(plain string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

// VerifyPassword 校验明文是否匹配哈希。
// hash 为空时与 dummy 哈希比较（恒定返回 false，但耗时与真实比较相当）。
func VerifyPassword(hash, plain string) bool {
	if hash == "" {
		hash = dummyPasswordHash
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

func mustDummyHash() string {
	h, err := bcrypt.GenerateFromPassword([]byte("regto-placeholder-never-matches"), bcrypt.DefaultCost)
	if err != nil {
		// 极端情况下（随机数不可用）回退为一个恒定串，功能仍不依赖它。
		return "$2a$10$0000000000000000000000000000000000000000000000000000"
	}
	return string(h)
}
