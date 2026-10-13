import { apiClient } from '../client'
import type {
  CreateTestPlanRequest,
  CreateTestTypeRequest,
  TestPlan,
  TestResult,
  TestType,
  UpdateTestPlanRequest,
  UpdateTestTypeRequest,
  TestCombinationCondition,
  TestProtectionThreshold,
  TestProtectionVote
} from '@/types'

export interface QualityProtectionEvidence {
  test_definition_id: number
  test_name: string
  result_id?: number
  started_at?: string
  current: boolean
  verdict: string
  automated_verdict: string
  admin_verdict: string
  reason: string
  automatic_reason: string
  thresholds: (TestProtectionThreshold & { observed: number | null })[]
  vote?: TestProtectionVote
  pass_count: number
  fail_count: number
}

export interface QualityProtectionHold {
  plan_id: number
  plan_name: string
  mode: 'combined' | 'per_test'
  reason: string
  updated_at: string
  prior_round: boolean
  rules: { id: string; name: string; condition?: TestCombinationCondition; current_match: string }[]
  tests: QualityProtectionEvidence[]
}

export async function getAccountProtection(id: number): Promise<QualityProtectionHold[]> {
  const { data } = await apiClient.get<QualityProtectionHold[]>(`/admin/accounts/${id}/quality-protection`)
  return data ?? []
}

export interface TestAdminReview {
  result: TestResult
  generation: number
  verdict: 'pending' | 'pass' | 'fail'
  admin_verdict: '' | 'pass' | 'fail'
  admin_user_id?: number
  decided_at?: string
  account_paused: boolean
}

export async function listReviews(): Promise<TestAdminReview[]> {
  const { data } = await apiClient.get<TestAdminReview[]>('/admin/test-reviews')
  return data ?? []
}

export async function decideResult(id: number, generation: number, verdict: 'pass' | 'fail'): Promise<void> {
  await apiClient.post(`/admin/test-results/${id}/decision`, { generation, verdict })
}

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
/** History limit applies per account/type/model/effort and outcome; active runs are always included. */
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

export const testsAPI = { listTypes, createType, updateType, deleteType, listPlans, createPlan, updatePlan, deletePlan, runPlan, listResults, deleteResult, retryResult, listReviews, decideResult, getAccountProtection }
export default testsAPI
