import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SharedPoolView from '../SharedPoolView.vue'

const api = vi.hoisted(() => ({
  getSharedPoolCards: vi.fn(), getMySharedCards: vi.fn(),
  getSharedWallet: vi.fn(), transferSharedEarnings: vi.fn()
}))
vi.mock('@/api/sharedPool', () => api)
const render = () => mount(SharedPoolView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' }, CreateAccountModal: true } } })
const card = {
  id: 1, platform: 'openai', display_name: 'Shared account', status: 'active',
  concurrency_limit: 3, concurrency_multiplier: 1, sell_rate: 1,
  total_call_count: 12000, recent_calls: [{ request_id: 'r1', model: 'model-a', result_status: 'success', duration_ms: 10, charged_amount: 1, created_at: '2026-09-09T00:00:00Z' }]
}

describe('SharedPoolView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.getSharedPoolCards.mockResolvedValue([card])
    api.getMySharedCards.mockResolvedValue([{ ...card, status: 'paused' }])
    api.getSharedWallet.mockResolvedValue({ available: '90.00000000', pending: '0', frozen: '0', total_earned: '90', total_transferred: '0' })
  })
  it('renders call counts and recent requests and loads paused owner accounts', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('article').text()).toContain((12000).toLocaleString())
    expect(wrapper.find('article').text()).toContain('model-a')
    await wrapper.findAll('button').find(b => b.text() === '我的账号')!.trigger('click')
    await flushPromises()
    expect(api.getMySharedCards).toHaveBeenCalledOnce()
    expect(wrapper.find('article').text()).toContain('已暂停')
  })
  it('keeps the transfer key when a response is lost and retries without double credit', async () => {
    api.transferSharedEarnings.mockRejectedValueOnce(new Error('Connection lost'))
      .mockResolvedValueOnce({ id: 1, amount: '90.00000000', balance_after: '100.00000000' })
    const wrapper = render(); await flushPromises()
    const transfer = wrapper.findAll('button').find(b => b.text() === '全部转入平台余额')!
    await transfer.trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Connection lost')
    await transfer.trigger('click'); await flushPromises()
    expect(api.transferSharedEarnings).toHaveBeenCalledTimes(2)
    expect(api.transferSharedEarnings.mock.calls[0]![0]).toBe(api.transferSharedEarnings.mock.calls[1]![0])
    expect(wrapper.text()).toContain('已转入 90.00000000')
  })
  it('uses shared creation and keeps the dialog open after partial batch success', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('上传账号'))!.trigger('click')
    const dialog = wrapper.findComponent({ name: 'CreateAccountModal' })
    expect(dialog.props('sharedPool')).toBe(true)
    expect(dialog.props('show')).toBe(true)
    dialog.vm.$emit('created')
    await flushPromises()
    expect(dialog.props('show')).toBe(true)
    dialog.vm.$emit('close')
    await flushPromises()
    expect(dialog.props('show')).toBe(false)
  })

})
