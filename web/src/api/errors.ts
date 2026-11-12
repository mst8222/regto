// problem+json → 人话（SPEC-WEB-001 REQ-06 / AC-06）。
//
// 为什么不直接把后端 detail 丢给用户：detail 是给接口调用方看的（多为英文技术描述），
// 而 AI 治理的几个 403 码用户根本无从下手——"AI_ACTION_REQUIRES_APPROVAL" 不告诉他
// 去哪儿批，这条错误就是死路。所以这里是**纯函数**映射表：可单测、可穷举、可演进。
//
// 契约：unknown code 一律回落到「展示后端 detail + request_id」，绝不编造解释。

import { ApiError } from './client'

/** 错误提示里的可行动作（目前只有一种：去审批队列）。 */
export type ErrorAction = { label: string; href: string }

export type ErrorAdvice = {
  /** 一句话说清发生了什么。 */
  title: string
  /** 后端原始描述，排障时要用，不能丢。 */
  detail: string
  code: string
  requestId?: string
  action?: ErrorAction
}

// AI 治理专属码的中文解释（SPEC-AGENT-002 的错误码体系）。
// 只解释"用户下一步能做什么"，不重复后端的技术细节。
const CODE_ADVICE: Record<string, { title: string; action?: ErrorAction }> = {
  // 这条必须有去处：AC-06 要求含跳转审批队列的引导。
  AI_ACTION_REQUIRES_APPROVAL: {
    title: '该操作需要人工审批后才能执行',
    action: { label: '去审批队列', href: '/approvals' },
  },
  AGENT_CAPABILITY_DENIED: {
    title: '当前 AI 会话不具备该操作的能力',
  },
  AGENT_SESSION_REVOKED: {
    title: '该 AI 会话已被撤销，需重新建立会话',
  },
  AI_APPROVAL_INVALID: {
    title: '审批记录无效：目标留痕可能已被决策或不存在',
  },
  ACL_DENIED: {
    title: '当前角色没有该操作的权限',
  },
}

/** describeApiError 把任意抛出物统一成可呈现的错误建议。 */
export function describeApiError(err: unknown): ErrorAdvice {
  if (!(err instanceof ApiError)) {
    const detail = err instanceof Error ? err.message : String(err)
    return { title: '请求失败', detail, code: 'UNKNOWN' }
  }
  const advice = CODE_ADVICE[err.code]
  return {
    title: advice?.title ?? '请求失败',
    detail: err.message,
    code: err.code,
    requestId: err.requestId,
    action: advice?.action,
  }
}

/** 已知 AI 治理码的集合，供测试穷举与文档同步检查。 */
export const knownGovernanceCodes = Object.keys(CODE_ADVICE)
