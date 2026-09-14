import { apiClient } from './client'
import type { TestResult } from '@/types'

export async function list(limit = 100): Promise<TestResult[]> {
  const { data } = await apiClient.get<TestResult[]>('/user/test-results', { params: { limit } })
  return data ?? []
}

export const testResultsAPI = { list }
export default testResultsAPI
