import { beforeEach, describe, expect, it, vi } from 'vitest'
import { emptyPerformanceConfig } from '@/types/accountPerformance'

const client=vi.hoisted(()=>({get:vi.fn(),post:vi.fn(),put:vi.fn(),delete:vi.fn()}))
vi.mock('@/api/client',()=>({apiClient:client}))
import { getPerformance, savePerformance, releasePerformance } from '@/api/admin/accounts'

beforeEach(()=>{for(const method of Object.values(client))method.mockReset().mockResolvedValue({data:{}})})

describe('account metric rules API',()=>{
 it('keeps local partial-history counts, actual window and measured zero when reading',async()=>{
  const evidence={success_rate:0,success_requests:0,error_requests:2,request_samples:2}
  const response={7:{account_id:7,config:emptyPerformanceConfig(),revision:4,metrics:evidence,window_start:'2026-11-06T00:00:00Z',window_end:'2026-11-06T01:00:00Z'}}
  client.post.mockResolvedValueOnce({data:response})
  const signal=new AbortController().signal
  await expect(getPerformance([7],signal)).resolves.toEqual(response)
  expect(client.post).toHaveBeenCalledWith('/admin/accounts/performance',{account_ids:[7]},{signal})
 })
 it('sends both upstream bounds with the full config and exact revision to the dedicated save endpoint',async()=>{
  const config={...emptyPerformanceConfig(),enabled:true,success_rate:{pause:{min:95,max:null},recovery:{min:99,max:null}}}
  await savePerformance(7,config,4)
  expect(client.put).toHaveBeenCalledOnce()
  expect(client.put).toHaveBeenCalledWith('/admin/accounts/7/performance',{config,revision:4})
  expect(client.post).not.toHaveBeenCalled()
 })
 it('releases only the owned performance pause with revision and generation',async()=>{
  await releasePerformance(7,4,2)
  expect(client.post).toHaveBeenCalledOnce()
  expect(client.post).toHaveBeenCalledWith('/admin/accounts/7/performance/release',{revision:4,pause_generation:2})
  expect(client.put).not.toHaveBeenCalled()
 })
})
