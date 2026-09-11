import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { OpsRequestDetail } from '@/api/admin/ops'
import RecentRequestsCell from '../RecentRequestsCell.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const success: OpsRequestDetail = {
  kind: 'success', request_id: 'req-success', created_at: '2026-09-11T10:27:52+08:00',
  user_id: 53, user_email: 'customer@example.com', group_id: 10, group_name: 'GPT-Luna',
  account_name: 'Sub2api-3-福利', api_key_name: 'Development', model: 'gpt-5.6-terra',
  stream: true, request_type: 'ws_v2', duration_ms: 38660, first_token_ms: 3060,
  input_tokens: 100, output_tokens: 1951, cache_read_tokens: 800, cache_creation_tokens: 100,
  actual_cost: 0.003141, account_cost: 0.078530,
}
const failure: OpsRequestDetail = {
  kind: 'error', request_id: 'req-error', error_id: 10, created_at: '2026-09-11T10:25:11+08:00',
  user_id: 53, user_email: 'customer@example.com', group_id: 10, group_name: 'GPT-Luna',
  status_code: 502, message: '<script>alert(1)</script> upstream failed', stream: false,
}
let wrapper: VueWrapper
const dialog = () => document.querySelector('[role="dialog"]')
const triggers = () => wrapper.findAll('[data-testid="recent-request-trigger"]')

beforeEach(() => {
  vi.useFakeTimers()
  wrapper = mount(RecentRequestsCell, { props: { requests: [success, failure] }, attachTo: document.body })
})
afterEach(() => {
  wrapper.unmount()
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('RecentRequestsCell', () => {
  it('lazily opens named identities, tokens, costs and latency on hover; newest is rightmost', async () => {
    expect(dialog()).toBeNull()
    expect(triggers()[0].classes()).toContain('bg-red-500')
    await triggers()[1].trigger('mouseenter')
    expect(dialog()?.textContent).toContain('customer@example.com')
    expect(dialog()?.textContent).toContain('#53')
    expect(dialog()?.textContent).toContain('Sub2api-3-福利')
    expect(dialog()?.textContent).toContain('gpt-5.6-terra')
    expect(dialog()?.textContent).toContain('WS v2')
    expect(dialog()?.textContent).toContain('1,951')
    expect(dialog()?.textContent).toContain('80.00%')
    expect(dialog()?.textContent).toContain('$0.003141')
    expect(dialog()?.textContent).toContain('$0.078530')
    expect(dialog()?.textContent).toContain('3.06s')
    expect(dialog()?.textContent).toContain('38.66s')
  })

  it('pins an already hovered request on click and preserves it while new requests arrive', async () => {
    await triggers()[1].trigger('mouseenter')
    await triggers()[1].trigger('click')
    await triggers()[1].trigger('mouseleave')
    await vi.advanceTimersByTimeAsync(200)
    expect(dialog()).not.toBeNull()
    const originalPanel = dialog()
    await wrapper.setProps({ requests: [{ ...success, request_id: 'req-new' }, success] })
    expect(dialog()).toBe(originalPanel)
    expect(dialog()?.textContent).toContain('req-success')
    await wrapper.setProps({ requests: [{ ...success, request_id: 'req-newer' }] })
    expect(dialog()).toBe(originalPanel)
    expect(dialog()?.textContent).toContain('req-success')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await nextTick()
    expect(dialog()).toBeNull()
  })

  it('allows moving into the panel, then closes on leaving or an outside click', async () => {
    await triggers()[1].trigger('mouseenter')
    await triggers()[1].trigger('mouseleave')
    dialog()?.dispatchEvent(new MouseEvent('mouseenter'))
    await vi.advanceTimersByTimeAsync(200)
    expect(dialog()).not.toBeNull()
    dialog()?.dispatchEvent(new MouseEvent('mouseleave'))
    await vi.advanceTimersByTimeAsync(200)
    expect(dialog()).toBeNull()
    await triggers()[1].trigger('click')
    document.body.click()
    await nextTick()
    expect(dialog()).toBeNull()
  })

  it('shows errors with named identities, missing usage as a dash, and escaped error text', async () => {
    await triggers()[0].trigger('click')
    expect(dialog()?.textContent).toContain('502')
    expect(dialog()?.textContent).toContain('customer@example.com')
    expect(dialog()?.textContent).toContain(failure.message)
    expect(dialog()?.textContent?.indexOf('admin.accounts.recentRequests.reason')).toBeLessThan(
      dialog()?.textContent?.indexOf('usage.time') ?? Number.MAX_SAFE_INTEGER,
    )
    expect(dialog()?.querySelector('script')).toBeNull()
    expect(dialog()?.querySelector('[data-testid="recent-request-tokens"]')?.textContent).toBe('——')
    expect(dialog()?.querySelector('[data-testid="recent-request-cache-rate"]')?.textContent).toBe('—')
    expect(dialog()?.textContent).not.toContain('$0.000000')
  })

  it('distinguishes measured zero from unavailable usage and excludes image input from text cache rate', async () => {
    await wrapper.setProps({ requests: [{ ...success, input_tokens: 200, image_input_tokens: 100, actual_cost: 0 }] })
    await triggers()[0].trigger('focus')
    expect(dialog()?.textContent).toContain('$0.000000')
    expect(dialog()?.querySelector('[data-testid="recent-request-cache-rate"]')?.textContent).toBe('80.00%')
  })

  it('keeps request indicators during refresh and handles missing names', async () => {
    await wrapper.setProps({ requests: [{ ...failure, user_email: undefined, group_name: undefined }], loading: true })
    expect(triggers()).toHaveLength(1)
    expect(wrapper.find('.animate-pulse').exists()).toBe(false)
    await triggers()[0].trigger('click')
    expect(dialog()?.querySelector('[data-testid="recent-request-user"]')?.textContent).toContain('#53')
    expect(dialog()?.querySelector('[data-testid="recent-request-group"]')).toBeNull()
  })
})
