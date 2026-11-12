// 菜单定义与角色粗筛（SPEC-WEB-001 REQ-07 / DESIGN-WEB-001 DEC-07）。
//
// 这里**不是**权限判定：把菜单藏起来只是减少"点了才被拒"的挫败感。
// 服务端 ACLGuard 才是唯一判定源；绕过前端直接调接口一样会 403。
// 分成纯函数是为了能被 Vitest 直接测——藏在组件里的 filter 只能靠 e2e 才验得到。

export type NavItem = {
  key: string
  label: string
  /** 允许看到该项的角色码；缺省 = 所有已登录主体可见。 */
  roles?: string[]
  /** 明确排除的角色。新增角色默认可见（多点一项无害），只对确定无权的关掉。 */
  denyRoles?: string[]
}

export const navItems: NavItem[] = [
  { key: '/stories', label: '需求' },
  // 审批队列：guest 连 agent.view 都没有，打开只会收到 403，直接不显示。
  { key: '/approvals', label: '审批队列', denyRoles: ['guest'] },
  { key: '/reports', label: '报表' },
  { key: '/mind', label: '协作画布' },
]

// visibleNavItems 按角色粗筛菜单。
//
// 零角色时只显示 public 项：**不回落 guest**（SPEC-ACCOUNT-001 的判定同样如此）。
// 回落成 guest 会让"尚未分配角色"的账号看到一堆本该只有访客才看的东西，
// 掩盖掉"这个账号还没配权限"这个真正需要暴露的问题。
export function visibleNavItems(items: NavItem[], roles: string[]): NavItem[] {
  return items.filter((item) => {
    if (item.roles && !(roles.length > 0 && item.roles.some((r) => roles.includes(r)))) {
      return false
    }
    if (item.denyRoles?.some((r) => roles.includes(r))) {
      return false
    }
    return true
  })
}

// canDecideApprovals 决定审批页的「批准/驳回」按钮是否渲染。
//
// 这是**粗筛**，不是权限判定：当前后端矩阵里 agent 资源的 edit 仅 admin 有
// （pm/dev/qa 只有 view），把按钮藏起来是为了不让人点了才被拒。
// 若将来矩阵给其他角色开了写权限而这里没同步，后果是该角色**看不到按钮**
// ——是体验退化，不是安全漏洞；反过来（前端放行、后端拒绝）也一样会 403。
// 真源始终是服务端 ACLGuard。
export function canDecideApprovals(roles: string[]): boolean {
  return roles.includes('admin')
}
