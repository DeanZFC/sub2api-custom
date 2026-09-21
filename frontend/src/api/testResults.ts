import { apiClient } from './client'
import type { TestResult, TestVote, TestVoteResult } from '@/types'

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

export async function votes(): Promise<TestVoteResult[]> {
  const { data } = await apiClient.get<TestVoteResult[]>('/user/test-votes')
  return data ?? []
}

export async function vote(id: number, vote: TestVote): Promise<TestVoteResult> {
  const { data } = await apiClient.post<TestVoteResult>(`/user/test-results/${id}/vote`, { vote })
  return data
}

export const testResultsAPI = { list, history, votes, vote }
export default testResultsAPI
