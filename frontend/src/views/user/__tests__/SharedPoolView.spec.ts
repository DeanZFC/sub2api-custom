import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SharedPoolView from '../SharedPoolView.vue'

const api = vi.hoisted(() => ({
  getSharedPoolCards: vi.fn(), getMySharedCards: vi.fn(),
  getSharedWallet: vi.fn(), transferSharedEarnings: vi.fn(),
  getSharedAccount: vi.fn(),
  listSharedAPIKeys: vi.fn(), getSharedAPIKeySecret: vi.fn(), setSharedListingStatus: vi.fn(), setSharedListingListed: vi.fn(), deleteSharedListing: vi.fn(),
  createSharedAPIKey: vi.fn(), updateSharedAPIKey: vi.fn(), deleteSharedAPIKey: vi.fn()
}))
vi.mock('@/api/sharedPool', () => api)
vi.mock('@/api/auth', () => ({ getPublicSettings: vi.fn().mockResolvedValue({ hide_ccs_import_button: false, api_base_url: 'https://example.com', site_name: 'test' }) }))
const render = () => mount(SharedPoolView, { global: { stubs: {
  AppLayout: { template: '<main><slot /></main>' },
  CreateAccountModal: true, EditAccountModal: true, ReAuthAccountModal: true, AccountTestModal: true, UseKeyModal: true,
  PlatformIcon: true, PlatformTypeBadge: true, Icon: true, CapacityBadge: true, RecentRequestsCell: true,
  UsageView: { props: ['embedded', 'sharedOnly'], template: '<div data-testid="shared-usage-view" />' },
  DataTable: { props: ['columns', 'data'], template: '<div><slot name="empty" /><slot /></div>' },
  BaseDialog: { props: ['show', 'title'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
  EmptyState: true
} } })
const card = {
  id: 1, account_id: 11, platform: 'openai', type: 'apikey', display_name: 'Shared account', status: 'active',
  concurrency_limit: 3, concurrency_multiplier: 1, sell_rate: 1.5,
  total_call_count: 12000, recent_calls: [{ request_id: 'r1', model: 'model-a', result_status: 'success', duration_ms: 10, charged_amount: 1, created_at: '2026-09-09T00:00:00Z' }]
}

describe('SharedPoolView', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.getSharedPoolCards.mockResolvedValue([card])
    api.getMySharedCards.mockResolvedValue([{ ...card, status: 'paused' }])
    api.getSharedWallet.mockResolvedValue({ available: '90.00000000', pending: '0', frozen: '0', total_earned: '90', total_transferred: '0' })
    api.listSharedAPIKeys.mockResolvedValue([])
    api.getSharedAccount.mockResolvedValue({
      id: 11, name: 'Shared account', platform: 'openai', type: 'apikey',
      concurrency: 3, rate_multiplier: 1.5, status: 'active', schedulable: true
    })
  })
  it('renders call counts, available models and collapsed recent requests', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.findAll('button').some(button => button.text() === '使用记录')).toBe(true)
    const article = wrapper.find('article')
    expect(article.text()).toContain((12000).toLocaleString())
    expect(article.text()).toContain('API Key')
    expect(article.text()).toContain('gpt-5.2')
    expect(article.text()).toContain('1.5x')
    expect(article.text()).toContain('最近请求')
    await wrapper.findAll('button').find(b => b.text() === '我的账号')!.trigger('click')
    await flushPromises()
    expect(api.getMySharedCards).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('已暂停')
  })

  it('switches to usage records inside the shared pool page', async () => {
    const wrapper = render(); await flushPromises()
    const usageButton = wrapper.findAll('button').find(button => button.text() === '使用记录')
    await usageButton?.trigger('click')
    expect(wrapper.find('[data-testid="shared-usage-view"]').exists()).toBe(true)
  })
  it('opens the account edit modal with the owned account', async () => {
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '我的账号')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '编辑')!.trigger('click')
    await flushPromises()
    expect(api.getSharedAccount).toHaveBeenCalledWith(11)
    const dialog = wrapper.findComponent({ name: 'EditAccountModal' })
    expect(dialog.props('sharedPool')).toBe(true)
    expect(dialog.props('account')).toMatchObject({ id: 11, type: 'apikey', name: 'Shared account' })
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
  it('creates a platform-only shared key without selecting accounts', async () => {
    api.createSharedAPIKey.mockResolvedValue({ id: 9, name: 'Pool key', key: 'sk-shared-test', key_preview: 'sk-sha…test', platform: 'openai', status: 'active', selection_mode: 'platform', priority_mode: 'rate', listing_ids: [] })
    const wrapper = render(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === '共享 API Key')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(b => b.text().includes('创建 Key'))!.trigger('click')
    await wrapper.get('input[maxlength="100"]').setValue('Pool key')
    await wrapper.findAll('button').find(b => b.text().includes('仅选平台'))!.trigger('click')
    await wrapper.findAll('button').find(b => b.text().includes('可用性优先'))!.trigger('click')
    const submit = wrapper.findAll('button').filter(b => b.text() === '创建 Key').at(-1)!
    await submit.trigger('click')
    await flushPromises()
    expect(api.createSharedAPIKey).toHaveBeenCalledWith({
      name: 'Pool key', platform: 'openai', selection_mode: 'platform', priority_mode: 'availability', listing_ids: []
    })
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
