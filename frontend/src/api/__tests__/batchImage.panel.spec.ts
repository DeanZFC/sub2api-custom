import { describe, expect, it, vi } from 'vitest'
import { submitBatchImageJob, listBatchImageJobs, downloadBatchImageZip } from '../batchImage'

const { request } = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { request } }))

describe('batch image panel authorization', () => {
  it('uses owned key IDs and preserves submission idempotency without a raw key', async () => {
    request.mockResolvedValue({ data: JSON.stringify({ id: 'batch-1' }), status: 200, headers: {} })
    const payload = { model: 'gemini-image', items: [{ custom_id: '1', prompt: 'test' }] }
    expect(await submitBatchImageJob(7, payload, 'submission-once')).toEqual({ id: 'batch-1' })
    expect(request).toHaveBeenLastCalledWith(expect.objectContaining({
      url: '/key-image-batches/7', method: 'POST', data: payload,
      headers: { 'Content-Type': 'application/json', 'Idempotency-Key': 'submission-once' },
    }))
    expect(request.mock.lastCall?.[0].headers).not.toHaveProperty('Authorization')
    await listBatchImageJobs(7, { status: 'completed', limit: 10 })
    expect(request.mock.lastCall?.[0].url).toBe('/key-image-batches/7?limit=10&status=completed')
  })

  it('retains structured errors and downloads binary results', async () => {
    request.mockResolvedValue({ data: JSON.stringify({ error: { code: 'DENIED', message: 'Access denied' } }), status: 403, headers: { 'X-Request-Id': 'req-1' } })
    await expect(listBatchImageJobs(8)).rejects.toMatchObject({ code: 'DENIED', status: 403, requestId: 'req-1', message: 'Access denied' })
    request.mockResolvedValue({ data: 'zip data', status: 200, headers: { 'Content-Type': 'application/zip' } })
    const blob = await downloadBatchImageZip(7, 'batch-1')
    expect(await blob.text()).toBe('zip data')
  })
})
