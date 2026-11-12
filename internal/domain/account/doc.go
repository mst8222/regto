// Package account 定义人类执行主体的权限模型：资源、动作、角色与默认权限矩阵。
//
// 本包零外部依赖（AGENTS.md §0 铁律 3）：只有标准库，不含 DB/HTTP/三方包。
// 判定是纯函数——HTTP 层负责取主体与查库，本包只回答「这个角色能不能做这件事」。
//
// 与 agent 包的分工：agent 管 AI 主体（能力白名单 + 三档策略 + 审批），
// account 管人类主体（角色 → 资源×动作矩阵）。两者按主体类型分流，互不叠加
// （DESIGN-ACCOUNT-001#DEC-06）。
//
// 规格来源：spec/account/rbac/SPEC-ACCOUNT-001.md。
package account
