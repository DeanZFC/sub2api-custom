import { apiClient } from '../client'
import type {
  CreateTestPlanRequest,
  CreateTestTypeRequest,
  TestPlan,
  TestResult,
  TestType,
  UpdateTestPlanRequest,
  UpdateTestTypeRequest
} from '@/types'

export async function listTypes(enabledOnly = true): Promise<TestType[]> {
  const { data } = await apiClient.get<TestType[]>('/admin/scheduled-test-definitions', {
    params: { enabled_only: enabledOnly }
  })
  return data ?? []
}
export async function createType(req: CreateTestTypeRequest): Promise<TestType> {
  const { data } = await apiClient.post<TestType>('/admin/scheduled-test-definitions', req)
  return data
}
export async function updateType(id: number, req: UpdateTestTypeRequest): Promise<TestType> {
  const { data } = await apiClient.put<TestType>(`/admin/scheduled-test-definitions/${id}`, req)
  return data
}
export async function deleteType(id: number): Promise<void> {
  await apiClient.delete(`/admin/scheduled-test-definitions/${id}`)
}

export async function listPlans(): Promise<TestPlan[]> {
  const { data } = await apiClient.get<TestPlan[]>('/admin/test-plans')
  return data ?? []
}
export async function createPlan(req: CreateTestPlanRequest): Promise<TestPlan> {
  const { data } = await apiClient.post<TestPlan>('/admin/test-plans', req)
  return data
}
export async function updatePlan(id: number, req: UpdateTestPlanRequest): Promise<TestPlan> {
  const { data } = await apiClient.put<TestPlan>(`/admin/test-plans/${id}`, req)
  return data
}
export async function deletePlan(id: number): Promise<void> {
  await apiClient.delete(`/admin/test-plans/${id}`)
}
export async function runPlan(id: number): Promise<void> {
  await apiClient.post(`/admin/test-plans/${id}/run`)
}
export async function listResults(planId: number, limit = 50): Promise<TestResult[]> {
  const { data } = await apiClient.get<TestResult[]>(`/admin/test-plans/${planId}/results`, { params: { limit } })
  return data ?? []
}

/** Delete one persisted test execution result. */
export async function deleteResult(id: number): Promise<void> {
  await apiClient.delete(`/admin/test-results/${id}`)
}
export async function retryResult(id: number): Promise<TestResult> {
  const { data } = await apiClient.post<TestResult>(`/admin/test-results/${id}/retry`)
  return data
}

export const testsAPI = { listTypes, createType, updateType, deleteType, listPlans, createPlan, updatePlan, deletePlan, runPlan, listResults, deleteResult, retryResult }
export default testsAPI
