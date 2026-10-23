import { apiClient } from './client'

export type BatchImageStatus =
  | 'queued'
  | 'running'
  | 'indexing'
  | 'processing_results'
  | 'settling'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'output_deleted'
  | string

export interface BatchImageSubmitItem {
  custom_id: string
  prompt: string
  output_count?: number
  reference_images?: BatchImageReferenceImage[]
}

export interface BatchImageReferenceImage {
  id?: string
  type?: string
  mime_type: string
  data?: string
  file_uri?: string
}

export interface BatchImageSubmitRequest {
  model: string
  task_name?: string
  parent_batch_id?: string
  provider?: '' | 'gemini_api' | 'vertex' | string
  image_size?: '1K' | '2K' | '4K' | string
  response_mime_type?: string
  aspect_ratio?: string
  items: BatchImageSubmitItem[]
  metadata?: Record<string, string>
}

export interface BatchImageJob {
  id: string
  object: string
  task_name: string
  parent_batch_id?: string | null
  status: BatchImageStatus
  model: string
  provider: string
  item_count: number
  success_count: number
  fail_count: number
  estimated_cost: number
  hold_amount: number
  actual_cost: number | null
  created_at: number
  submitted_at: number | null
  settled_at: number | null
  downloaded_at?: number | null
  output_deleted_at?: number | null
}

export interface BatchImageItem {
  batch_id?: string
  source_task_name?: string
  custom_id: string
  status: string
  prompt_preview?: string | null
  mime_type: string | null
  file_extension: string | null
  image_count: number
  error?: {
    code: string
    message: string
    source?: 'provider' | 'system' | string
  } | null
}

export interface BatchImageItemsResponse {
  object: string
  data: BatchImageItem[]
  has_more: boolean
}

export interface BatchImageJobsResponse {
  object: string
  data: BatchImageJob[]
  has_more: boolean
}

export interface BatchImageModel {
  id: string
  object: string
  provider: string
}

export interface BatchImageModelsResponse {
  object: string
  data: BatchImageModel[]
}

export interface BatchImageJobsListOptions {
  limit?: number
  cursor?: string
  status?: string
  taskName?: string
  downloaded?: '' | 'true' | 'false' | string
  from?: string
  to?: string
}

async function parseBatchImageError(response: Response): Promise<Error> {
  try {
    const body = await response.json()
    const message = body?.error?.message || body?.message || response.statusText
    const error = new Error(message)
    ;(error as any).code = body?.error?.code || response.status
    ;(error as any).status = response.status
    ;(error as any).requestId = response.headers.get('X-Request-Id') || ''
    return error
  } catch {
    const error = new Error(response.statusText || `HTTP ${response.status}`)
    ;(error as any).code = response.status
    ;(error as any).status = response.status
    ;(error as any).requestId = response.headers.get('X-Request-Id') || ''
    return error
  }
}

// Panel requests use the login session and an owned key ID; no API secret is
// downloaded from the key list or persisted in the browser.
async function panelBatchRequest(apiKey: number, suffix = '', options: RequestInit = {}): Promise<Response> {
  const result = await apiClient.request({
    url: `/key-image-batches/${apiKey}${suffix}`,
    method: options.method || 'GET',
    headers: options.headers as Record<string, string>,
    data: options.body ? JSON.parse(String(options.body)) : undefined,
    responseType: 'blob',
    validateStatus: (status) => status !== 401,
  })
  const response = new Response(result.status === 204 ? null : result.data, { status: result.status, headers: result.headers as HeadersInit })
  if (!response.ok) throw await parseBatchImageError(response)
  return response
}

export async function submitBatchImageJob(
  apiKey: number,
  payload: BatchImageSubmitRequest,
  idempotencyKey: string,
): Promise<BatchImageJob> {
  const response = await panelBatchRequest(apiKey, '', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Idempotency-Key': idempotencyKey,
    },
    body: JSON.stringify(payload),
  })
  return response.json()
}

export async function getBatchImageJob(apiKey: number, batchId: string): Promise<BatchImageJob> {
  const response = await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}`)
  return response.json()
}

export async function listBatchImageJobs(apiKey: number, options: number | BatchImageJobsListOptions = 20): Promise<BatchImageJobsResponse> {
  const params = new URLSearchParams()
  if (typeof options === 'number') {
    params.set('limit', String(options))
  } else {
    params.set('limit', String(options.limit || 20))
    if (options.cursor) params.set('cursor', options.cursor)
    if (options.status) params.set('status', options.status)
    if (options.taskName) params.set('task_name', options.taskName)
    if (options.downloaded) params.set('downloaded', options.downloaded)
    if (options.from) params.set('from', options.from)
    if (options.to) params.set('to', options.to)
  }
  const response = await panelBatchRequest(apiKey, `?${params.toString()}`)
  return response.json()
}

export async function listBatchImageModels(apiKey: number): Promise<BatchImageModelsResponse> {
  const response = await panelBatchRequest(apiKey, '/models')
  return response.json()
}

export async function listBatchImageItems(
  apiKey: number,
  batchId: string,
  status = '',
): Promise<BatchImageItemsResponse> {
  const query = status ? `?status=${encodeURIComponent(status)}` : ''
  const response = await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}/items${query}`)
  return response.json()
}

export async function cancelBatchImageJob(apiKey: number, batchId: string): Promise<BatchImageJob> {
  const response = await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}/cancel`, {
    method: 'POST',
  })
  return response.json()
}

export async function downloadBatchImageZip(apiKey: number, batchId: string): Promise<Blob> {
  const response = await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}/download`)
  return response.blob()
}

export async function getBatchImageItemContent(apiKey: number, batchId: string, customId: string, imageIndex = 0): Promise<Blob> {
  const response = await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}/items/${encodeURIComponent(customId)}/content?image_index=${encodeURIComponent(String(imageIndex))}`)
  return response.blob()
}

export async function deleteBatchImageJobRecord(apiKey: number, batchId: string): Promise<void> {
  await panelBatchRequest(apiKey, `/${encodeURIComponent(batchId)}`, {
    method: 'DELETE',
  })
}

export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}
