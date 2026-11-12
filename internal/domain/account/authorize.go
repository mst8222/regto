package account

// CodeACLDenied 是人类越权的机器可读错误码（HTTP 403，RFC 7807）。
const CodeACLDenied = "ACL_DENIED"

// 判定理由。对外可见（写进 problem.detail 与日志），所以是稳定字符串而非 iota：
// 排障的人靠它一眼分辨「没配角色」和「角色不够」。
const (
	ReasonAllowed         = "ALLOWED"          // 放行
	ReasonUnknownResource = "UNKNOWN_RESOURCE" // 资源未登记：拒绝，而不是默认放行
	ReasonUnknownAction   = "UNKNOWN_ACTION"   // 动作未登记
	ReasonImmutable       = "IMMUTABLE"        // 物理约束：留痕不可改不可删
	ReasonNoRole          = "NO_ROLE"          // 没有任何有效角色（不回落 guest）
	ReasonNotGranted      = "NOT_GRANTED"      // 角色存在，但矩阵不放行
)

// immutableActions 是存储层已经物理禁止的动作（SPEC-ACCOUNT-001 REQ-02）。
//
// `traces` 在迁移 000019 之后对应用账号只有 SELECT/INSERT：UPDATE/DELETE 会被
// PostgreSQL 直接拒绝。权限层必须**承认**这个事实——否则 admin 会拿到
// 「ACL 说可以、存储说不行」的体验，那是最糟的一种不一致。
//
// 这里只做语义对齐，不是唯一防线（ASM-03）：数据库权限仍在，绕过应用也写不进去。
var immutableActions = map[Resource]ActionMask{
	ResourceTrace: ActionMask(ActionEdit) | ActionMask(ActionDelete),
}

// IsImmutable 报告某资源上的某动作是否被物理约束禁止。
func IsImmutable(res Resource, act Action) bool {
	m, ok := immutableActions[res]
	if !ok {
		return false
	}
	return m.Has(act)
}

// Decision 是一次授权判定的结论。
type Decision struct {
	Allowed bool
	Reason  string
	By      Role // 命中的角色（Allowed 时有效，便于排障「到底是哪个角色给的权」）
}

// Denied 是构造拒绝结论的便捷函数。
func Denied(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// Authorize 判断一组角色能否对某资源执行某动作（SPEC-ACCOUNT-001 REQ-02）。
//
// 判定顺序（DESIGN-ACCOUNT-001 §4，顺序本身是需求）：
//  1. 资源未登记 → 拒绝：新增域时忘了登记矩阵，应是「谁都不能用」，不是「谁都能用」
//  2. 动作未登记 → 拒绝
//  3. 物理约束命中 → 拒绝（IMMUTABLE）：先于矩阵，保证与存储层事实一致
//  4. 没有任何有效角色 → 拒绝（NO_ROLE）：不回落 guest，「忘了配」不能被静默当成只读
//  5. 任一角色放行 → 允许（并集，多给角色不降权）
func Authorize(roles []Role, res Resource, act Action) Decision {
	if !IsKnownResource(res) {
		return Denied(ReasonUnknownResource)
	}
	if !IsKnownAction(act) {
		return Denied(ReasonUnknownAction)
	}
	if IsImmutable(res, act) {
		return Denied(ReasonImmutable)
	}

	// 脏数据防御：库里可能残留已废弃的角色 code，跳过而不是让整次判定失败。
	effective := make([]Role, 0, len(roles))
	for _, r := range roles {
		if IsKnownRole(r) {
			effective = append(effective, r)
		}
	}
	if len(effective) == 0 {
		return Denied(ReasonNoRole)
	}

	for _, r := range effective {
		if Allows(r, res, act) {
			return Decision{Allowed: true, Reason: ReasonAllowed, By: r}
		}
	}
	return Denied(ReasonNotGranted)
}
