import { apiClient } from './client'
import type { TestResult } from '@/types'

export async function list(limit = 3): Promise<TestResult[]> {
  const { data } = await apiClient.get<TestResult[]>('/user/test-results', { params: { limit } })
  return data ?? []
}

export interface TestResultHistory {
  items: TestResult[]
  next_before_id?: number
}

export async function history(id: number, beforeId?: number): Promise<TestResultHistory> {
  const { data } = await apiClient.get<TestResultHistory>(`/user/test-results/${id}/history`, {
    params: { limit: 20, before_id: beforeId },
  })
  return data
}

export const testResultsAPI = { list, history }
export default testResultsAPI
