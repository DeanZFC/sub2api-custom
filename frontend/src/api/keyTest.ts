import { apiClient } from './client'

export interface KeyTestResult {
  model: string
  choices: Array<{ message: { content?: string | null; reasoning_content?: string | null }; finish_reason?: string }>
  usage?: { prompt_tokens?: number; completion_tokens?: number; total_tokens?: number }
}

/** Uses the authenticated owner's key ID; never requests or stores the secret. */
export async function getKeyTestModels(keyId: number, signal?: AbortSignal): Promise<string[]> {
  const { data } = await apiClient.get<{ data: Array<{ id: string }> }>('/key-tests/' + keyId + '/models', { signal })
  return [...new Set((data.data ?? []).map(model => model.id).filter(Boolean))]
}

export async function runKeyTest(keyId: number, model: string, signal?: AbortSignal): Promise<KeyTestResult> {
  const { data } = await apiClient.post<KeyTestResult>('/key-tests/' + keyId, { model }, {
    signal,
    timeout: 120000,
  })
  return data
}

export function keyTestErrorMessage(error: unknown, fallback: string): string {
  const value = error as { error?: { message?: string }; message?: string } | undefined
  return value?.error?.message || value?.message || fallback
}
