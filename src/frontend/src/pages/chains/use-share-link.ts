import { useCallback, useRef, useState } from 'react'

import { api, errorMessage } from '@/lib/api'
import type { ChainShareLink, SubUser } from '@/lib/types'

export interface ShareLinkTarget {
  kind: 'chain' | 'node'
  id: number
  name: string
  /** 非共享入口链的出口业务节点：其订阅凭据走 node_ids 分配，用于预选用户。 */
  exitNodeId?: number
}

/** 与订阅交付同一门槛：过期/禁用/配额耗尽/未生效的用户无法生成有效链接。 */
export function userShareEligible(u: SubUser): boolean {
  if (u.expired || u.disabled || u.quota_exhausted) return false
  if (u.effective_at && new Date(u.effective_at).getTime() > Date.now()) return false
  return true
}

/** 用户是否持有该目标的访问权（仅用于预选排序；后端仍会做权威校验）。 */
function userHasAccess(u: SubUser, target: ShareLinkTarget): boolean {
  if (target.kind === 'node') return u.node_ids.includes(target.id)
  if (u.effective_chain_ids.includes(target.id) || u.chain_ids.includes(target.id)) return true
  return target.exitNodeId !== undefined && u.node_ids.includes(target.exitNodeId)
}

/**
 * 已分配用户优先，其余有效用户兜底（后端会拒绝未分配的导出并给出原因）。
 */
export function pickShareUser(users: SubUser[], target: ShareLinkTarget): SubUser | null {
  const eligible = users.filter(userShareEligible)
  const preferred = eligible.filter((u) => userHasAccess(u, target))
  return preferred[0] ?? eligible[0] ?? null
}

/** 分享链接导出对话框的状态与数据加载；错误在对话框内展示。 */
export function useShareLink() {
  const [target, setTarget] = useState<ShareLinkTarget | null>(null)
  const [users, setUsers] = useState<SubUser[]>([])
  const [userId, setUserId] = useState<number | null>(null)
  const [link, setLink] = useState<ChainShareLink | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const usersRequest = useRef(0)

  const fetchLink = useCallback(async (t: ShareLinkTarget, uid: number) => {
    setLoading(true)
    setError('')
    try {
      setLink(
        t.kind === 'chain'
          ? await api.chainShareLink(t.id, uid)
          : await api.nodeShareLink(t.id, uid),
      )
    } catch (err) {
      setLink(null)
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [])

  const open = useCallback(
    async (t: ShareLinkTarget) => {
      setTarget(t)
      setLink(null)
      setError('')
      setUserId(null)
      const request = ++usersRequest.current
      try {
        const list = (await api.users({ display: 'silent' })) ?? []
        if (request !== usersRequest.current) return
        const candidates = list.filter(userShareEligible)
        setUsers(candidates)
        const picked = pickShareUser(candidates, t)
        if (!picked) {
          setError('没有订阅状态有效的用户，无法生成链接')
          return
        }
        setUserId(picked.id)
        void fetchLink(t, picked.id)
      } catch (err) {
        if (request !== usersRequest.current) return
        setError(errorMessage(err))
      }
    },
    [fetchLink],
  )

  const selectUser = useCallback(
    (id: number) => {
      if (!target) return
      setUserId(id)
      void fetchLink(target, id)
    },
    [fetchLink, target],
  )

  const close = useCallback(() => setTarget(null), [])

  return { target, users, userId, link, loading, error, open, selectUser, close }
}

export type ShareLinkController = ReturnType<typeof useShareLink>
