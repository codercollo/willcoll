import { defineStore } from 'pinia'
import type { PageMeta } from '~/types/api'
import type { AdminManager, AdminPlan, AdminSubscription, PlanInput, SystemHealth } from '~/types/admin'

export const useAdmin = defineStore('admin', () => {
  const api = useApi()

  async function managers(q: { status?: string; page?: number; sort?: string } = {}) {
    const res = await api.get<{ managers: AdminManager[]; metadata: Partial<PageMeta> }>('/admin/managers', { page_size: 20, ...q })
    return { rows: res.managers ?? [], meta: res.metadata }
  }
  async function manager(id: string) {
    return (await api.get<{ manager: AdminManager }>(`/admin/managers/${id}`)).manager
  }
  /** Suspending revokes the firm's sessions at once. */
  async function suspend(id: string) { await api.post(`/admin/managers/${id}/suspend`) }
  async function reinstate(id: string) { await api.post(`/admin/managers/${id}/reinstate`) }

  async function subscriptions(q: { status?: string; page?: number } = {}) {
    const res = await api.get<{ subscriptions: AdminSubscription[]; metadata: Partial<PageMeta> }>('/admin/subscriptions', { page_size: 20, ...q })
    return { rows: res.subscriptions ?? [], meta: res.metadata }
  }

  async function plans() { return (await api.get<{ plans: AdminPlan[] }>('/admin/plans')).plans ?? [] }
  async function createPlan(input: PlanInput) {
    return (await api.post<{ plan: AdminPlan }>('/admin/plans', input)).plan
  }
  /** Partial update; a new price applies to invoices raised afterwards only. */
  async function updatePlan(id: string, patch: PlanInput) {
    return (await api.patch<{ plan: AdminPlan }>(`/admin/plans/${id}`, patch)).plan
  }
  /** Archiving hides a plan from managers; existing subscribers keep their period. */
  async function archivePlan(id: string) { return (await api.post<{ plan: AdminPlan }>(`/admin/plans/${id}/archive`)).plan }
  async function restorePlan(id: string) { return (await api.post<{ plan: AdminPlan }>(`/admin/plans/${id}/restore`)).plan }

  async function health() { return api.get<SystemHealth>('/admin/system/health') }

  return { managers, manager, suspend, reinstate, subscriptions, plans, createPlan, updatePlan, archivePlan, restorePlan, health }
})
