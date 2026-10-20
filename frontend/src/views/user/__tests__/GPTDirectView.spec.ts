import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import GPTDirectView from '../GPTDirectView.vue'

const api = vi.hoisted(() => ({ config: vi.fn(), verify: vi.fn(), subscription: vi.fn(), redeem: vi.fn(), task: vi.fn(), query: vi.fn() }))
vi.mock('@/api/gptDirect', () => ({ gptDirectAPI: api }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 7 } }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key, te: () => true }) }))

const token = 'a'.repeat(43)
const task = { id: 'task_1', status: 'queued', progress: 0, product: 'gpt', plan: 'plus', planName: 'ChatGPT Plus', planKind: 'subscription', email: 'test@example.invalid' }
const storageKey = 'gpt-direct:7:scope1'
const session = '{"sessionToken":"session-canary","accessToken":"access-canary","user":{"email":"test@example.invalid"}}'
let wrappers: VueWrapper[] = []

async function create() {
  const wrapper = mount(GPTDirectView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } })
  wrappers.push(wrapper)
  await flushPromises()
  return wrapper
}
async function confirmScreen(wrapper: VueWrapper) {
  await wrapper.get('#gpt-code').setValue(' CODE-123 ')
  await wrapper.findAll('form')[0]!.trigger('submit')
  await flushPromises()
  await vi.waitFor(() => expect(wrapper.find('#gpt-session').exists()).toBe(true))
  await wrapper.get('#gpt-session').setValue(session)
  await wrapper.findAll('form')[0]!.trigger('submit')
  await flushPromises()
}
async function finishAsync() {
  for (let i = 0; i < 12; i++) {
    await new Promise(resolve => setTimeout(resolve, 5))
    await flushPromises()
  }
}

describe('GPT Direct redemption and recovery', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.stubGlobal('crypto', webcrypto)
    sessionStorage.clear()
    api.config.mockResolvedValue({ enabled: true, receipt_scope: 'scope1', purchase_url: 'https://shop.example.com/cdk', support_url: '' })
    api.verify.mockResolvedValue({ valid: true, product: 'gpt', plan: 'plus', planName: 'ChatGPT Plus', status: 'available' })
    api.subscription.mockResolvedValue({ email: 'test@example.invalid', plan: 'free', expiresAt: null })
    api.redeem.mockResolvedValue({ task, token })
    api.task.mockResolvedValue(task)
  })
  afterEach(() => {
    wrappers.forEach(wrapper => wrapper.unmount())
    wrappers = []
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('shows the purchase link and only submits after explicit account confirmation', async () => {
    const wrapper = await create()
    expect(wrapper.get('a[href="https://shop.example.com/cdk"]').attributes('rel')).toContain('noreferrer')
    await confirmScreen(wrapper)
    expect(api.redeem).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="gpt-submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="gpt-confirm"]').setValue(true)
    await wrapper.get('[data-testid="gpt-submit"]').trigger('click')
    await finishAsync()
    expect(api.redeem).toHaveBeenCalledTimes(1)
    expect(api.redeem).toHaveBeenCalledWith('CODE-123', session, expect.any(String))
    expect(wrapper.find('#gpt-session').exists()).toBe(false)
    const stored = sessionStorage.getItem(storageKey)!
    expect(JSON.parse(stored).task).toEqual({ id: 'task_1', token })
    expect(stored).not.toContain('canary')
    expect(stored).not.toContain('CODE-123')
    expect(stored).not.toContain('example.invalid')
  })

  it('reuses the original idempotency key after timeout and page reload without persisting credentials', async () => {
    api.redeem.mockRejectedValue({ status: 502, reason: 'UPSTREAM_UNAVAILABLE' })
    const first = await create()
    await confirmScreen(first)
    await first.get('[data-testid="gpt-confirm"]').setValue(true)
    await first.get('[data-testid="gpt-submit"]').trigger('click')
    await finishAsync()
    expect(api.redeem).toHaveBeenCalledTimes(1)
    const originalKey = api.redeem.mock.calls[0]![2]
    expect(sessionStorage.getItem(storageKey)).not.toContain('CODE-123')
    expect(sessionStorage.getItem(storageKey)).not.toContain('canary')
    first.unmount()
    wrappers = []
    const second = await create()
    expect(second.text()).toContain('gptDirect.pendingHint')
    await confirmScreen(second)
    expect(api.verify).toHaveBeenLastCalledWith('CODE-123', originalKey)
    await second.get('[data-testid="gpt-confirm"]').setValue(true)
    await second.get('[data-testid="gpt-submit"]').trigger('click')
    await finishAsync()
    expect(api.redeem).toHaveBeenCalledTimes(2)
    expect(api.redeem.mock.calls[1]![2]).toBe(originalKey)
  })

  it('restores a task, stops polling after completion and never resubmits', async () => {
    vi.useFakeTimers()
    sessionStorage.setItem(storageKey, JSON.stringify({ task: { id: 'task_1', token } }))
    api.task.mockResolvedValue({ ...task, status: 'success' })
    const wrapper = await create()
    expect(api.task).toHaveBeenCalledWith('task_1', token)
    expect(wrapper.text()).toContain('gptDirect.status.success')
    await vi.advanceTimersByTimeAsync(60000)
    expect(api.task).toHaveBeenCalledTimes(1)
    expect(api.redeem).not.toHaveBeenCalled()
  })

  it('backs off polling on 429 and preserves the receipt', async () => {
    vi.useFakeTimers()
    sessionStorage.setItem(storageKey, JSON.stringify({ task: { id: 'task_1', token } }))
    api.task.mockRejectedValueOnce({ status: 429, metadata: { retry_after: '600' } }).mockResolvedValue(task)
    await create()
    expect(api.task).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(599000)
    expect(api.task).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(api.task).toHaveBeenCalledTimes(2)
    expect(sessionStorage.getItem(storageKey)).toContain('task_1')
  })

  it('does not reveal or use receipts stored for another user or channel', async () => {
    sessionStorage.setItem('gpt-direct:8:scope1', JSON.stringify({ task: { id: 'task_1', token } }))
    sessionStorage.setItem('gpt-direct:7:another-scope', JSON.stringify({ task: { id: 'task_1', token } }))
    const wrapper = await create()
    expect(api.task).not.toHaveBeenCalled()
    expect(wrapper.find('#gpt-code').exists()).toBe(true)
  })

  it('rejects malformed Session JSON and blocks expired credentials before confirmation', async () => {
    const wrapper = await create()
    await wrapper.get('#gpt-code').setValue('CODE-123')
    await wrapper.findAll('form')[0]!.trigger('submit')
    await flushPromises()
    await wrapper.get('#gpt-session').setValue('{bad-json')
    await wrapper.findAll('form')[0]!.trigger('submit')
    await flushPromises()
    expect(api.subscription).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('gptDirect.errors.INVALID_SESSION')
    api.subscription.mockRejectedValue({ reason: 'SESSION_EXPIRED', status: 400 })
    await wrapper.get('#gpt-session').setValue(session)
    await wrapper.findAll('form')[0]!.trigger('submit')
    await flushPromises()
    expect(wrapper.find('[data-testid="gpt-submit"]').exists()).toBe(false)
    expect(api.redeem).not.toHaveBeenCalled()
  })

  it('keeps the explicit unverified-account acknowledgement when subscription lookup is unavailable', async () => {
    api.subscription.mockRejectedValue({ status: 502 })
    const wrapper = await create()
    await confirmScreen(wrapper)
    expect(wrapper.text()).toContain('gptDirect.confirmUnverified')
    expect(wrapper.get('[data-testid="gpt-submit"]').attributes('disabled')).toBeDefined()
    expect(api.redeem).not.toHaveBeenCalled()
  })

  it('does not expose forms when disabled and does not render unsafe purchase URLs', async () => {
    api.config.mockResolvedValueOnce({ enabled: false })
    const disabled = await create()
    expect(disabled.find('form').exists()).toBe(false)
    api.config.mockResolvedValueOnce({ enabled: true, receipt_scope: 'scope1', purchase_url: 'javascript:alert(1)' })
    const enabled = await create()
    expect(enabled.find('a[href^="javascript:"]').exists()).toBe(false)
  })

  it.each([
    'QQ：123456789\n微信：example-support',
    '<img src=x onerror=alert(1)>',
    'javascript:alert(1)',
    '客服说明\nhttps://support.example.com',
  ])('renders support information as plain text: %s', async content => {
    api.config.mockResolvedValueOnce({ enabled: true, support_url: content })
    const wrapper = await create()
    expect(wrapper.text()).toContain(content)
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('a').exists()).toBe(false)
  })

  it('keeps existing support URLs clickable', async () => {
    api.config.mockResolvedValueOnce({ enabled: true, support_url: 'https://support.example.com/contact' })
    const wrapper = await create()
    const link = wrapper.get('a[href="https://support.example.com/contact"]')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toContain('noreferrer')
  })
  it('shows the configured offer and a server-confirmed grant without redeeming again', async () => {
    api.config.mockResolvedValueOnce({ enabled: true, receipt_scope: 'scope1', lottery_reward_enabled: true, lottery_chances: 2 })
    sessionStorage.setItem(storageKey, JSON.stringify({ task: { id: 'task_1', token } }))
    api.task.mockResolvedValueOnce({ ...task, status: 'success', lottery_reward: { status: 'awarded', chances: 2 } })
    const wrapper = await create()
    expect(wrapper.text()).toContain('gptDirect.lotteryOffer')
    expect(wrapper.text()).toContain('gptDirect.lotteryReward.awarded')
    expect(api.redeem).not.toHaveBeenCalled()
  })
})
