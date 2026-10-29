import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { nextTick } from 'vue'
import type { OpsAccountRequestStats, OpsRequestDetail } from '@/api/admin/ops'
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
const statistics: OpsAccountRequestStats = {
  window_start: '2026-09-11T09:30:00+08:00', window_end: '2026-09-11T10:30:00+08:00',
  cache_rate: 0.8, avg_first_token_ms: 3060, avg_duration_ms: 38660,
  cache_samples: 32, first_token_samples: 30, duration_samples: 35,
}
let wrapper: VueWrapper
const dialog = () => document.querySelector('[role="dialog"]')
const triggers = () => wrapper.findAll('[data-testid="recent-request-trigger"]')

beforeEach(() => {
  vi.useFakeTimers()
  wrapper = mount(RecentRequestsCell, { props: { requests: [success, failure], statistics }, attachTo: document.body })
})
afterEach(() => {
  wrapper.unmount()
  document.body.innerHTML = ''
  vi.useRealTimers()
})

describe('RecentRequestsCell', () => {
  it('shows hourly aggregate metrics above the timeline while keeping the latest request time', () => {
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('80.0%')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('3.06s')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('38.66s')
    expect(wrapper.get('[data-testid="recent-requests-metrics"]').element.compareDocumentPosition(triggers()[0].element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(wrapper.text()).toContain('10:27:52')
    expect(dialog()).toBeNull()
  })

  it('renders the complete hourly statistics independently of the timeline request telemetry', async () => {
    await wrapper.setProps({
      requests: [{ ...success, input_tokens: 0, cache_read_tokens: 10000, first_token_ms: 99999, duration_ms: 99999 }],
      statistics: { ...statistics, cache_rate: 0.08, avg_first_token_ms: 2000, avg_duration_ms: 8000 },
    })
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('8.0%')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('2.00s')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('8.00s')
  })

  it('preserves measured zero and updates unavailable statistics to a dash on refresh', async () => {
    await wrapper.setProps({ statistics: {
      ...statistics, cache_rate: 0, avg_first_token_ms: 0, avg_duration_ms: 0,
    } })
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('0.0%')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('0ms')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('0ms')
    await wrapper.setProps({ statistics: {
      ...statistics, cache_rate: null, avg_first_token_ms: null, avg_duration_ms: null,
      cache_samples: 0, first_token_samples: 0, duration_samples: 0,
    } })
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('-')
  })

  it('limits the timeline to ten requests without truncating the hourly summary', async () => {
    const requests = Array.from({ length: 21 }, (_, index) => ({
      ...success, request_id: `req-${index}`, input_tokens: 100,
      cache_read_tokens: 0, cache_creation_tokens: 0,
      first_token_ms: 1000, duration_ms: 2000,
    }))
    await wrapper.setProps({ requests })
    expect(triggers()).toHaveLength(10)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('80.0%')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('3.06s')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('38.66s')
    await triggers()[0].trigger('click')
    expect(dialog()?.textContent).toContain('req-9')
    await triggers()[9].trigger('click')
    expect(dialog()?.textContent).toContain('req-0')
  })

  it('shows ten loading placeholders and hides the summary when neither data source has loaded', async () => {
    await wrapper.setProps({ requests: [], statistics: undefined, loading: true, statisticsLoading: true })
    expect(wrapper.findAll('.animate-pulse')).toHaveLength(10)
    expect(wrapper.find('[data-testid="recent-requests-metrics"]').exists()).toBe(false)
  })

  it.each([null, undefined])('keeps the timeline but does not derive hourly metrics from requests when statistics are %s', async statistics => {
    await wrapper.setProps({ statistics })
    expect(triggers()).toHaveLength(2)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('-')
  })

  it('shows hourly metrics when there are no timeline requests', async () => {
    await wrapper.setProps({ requests: [] })
    expect(triggers()).toHaveLength(0)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('80.0%')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('3.06s')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('38.66s')
    expect(wrapper.text()).not.toContain('10:27:52')
  })

  it('shows dashes for an empty hourly window even when historical requests have metrics', async () => {
    await wrapper.setProps({ statistics: {
      ...statistics, cache_rate: null, avg_first_token_ms: null, avg_duration_ms: null,
      cache_samples: 0, first_token_samples: 0, duration_samples: 0,
    } })
    expect(triggers()).toHaveLength(2)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('-')
  })

  it('preserves a successful hourly snapshot while refreshing and clears failed statistics', async () => {
    await wrapper.setProps({ statisticsLoading: true })
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('80.0%')
    await wrapper.setProps({ statisticsLoadError: true })
    expect(triggers()).toHaveLength(2)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-metrics"]').attributes('title')).toContain('admin.accounts.recentRequests.metricsLoadFailed')
    await wrapper.setProps({ statisticsLoadError: false, statisticsLoading: false })
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('80.0%')
  })

  it('does not derive metrics from timeline requests while the first hourly snapshot is loading', async () => {
    await wrapper.setProps({ statistics: undefined, statisticsLoading: true })
    expect(triggers()).toHaveLength(2)
    expect(wrapper.get('[data-testid="recent-requests-cache-rate"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-first-token"]').text()).toBe('-')
    expect(wrapper.get('[data-testid="recent-requests-duration"]').text()).toBe('-')
  })

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
    expect(dialog()?.querySelector('[data-testid="recent-request-cache-rate"]')?.textContent).toBe('-')
    expect(dialog()?.textContent).not.toContain('$0.000000')
  })

  it('distinguishes measured zero from unavailable usage and excludes image input from text cache rate', async () => {
    await wrapper.setProps({ requests: [{ ...success, input_tokens: 200, image_input_tokens: 100, actual_cost: 0 }] })
    await triggers()[0].trigger('focus')
    expect(dialog()?.textContent).toContain('$0.000000')
    expect(dialog()?.querySelector('[data-testid="recent-request-cache-rate"]')?.textContent).toBe('80.00%')
  })

  it('keeps request indicators during refresh and handles missing names', async () => {
    await wrapper.setProps({ requests: [{ ...failure, user_email: undefined, group_name: undefined }], loading: true, loadError: true })
    expect(triggers()).toHaveLength(1)
    expect(wrapper.find('.animate-pulse').exists()).toBe(false)
    await triggers()[0].trigger('click')
    expect(dialog()?.querySelector('[data-testid="recent-request-user"]')?.textContent).toContain('#53')
    expect(dialog()?.querySelector('[data-testid="recent-request-group"]')).toBeNull()
  })

  it('shows load failure before the loading skeleton, then preserves the normal empty state after recovery', async () => {
    await wrapper.setProps({ requests: [], statistics: undefined, loading: true, loadError: true })
    expect(wrapper.text()).toContain('admin.accounts.recentRequests.loadFailed')
    expect(wrapper.find('.animate-pulse').exists()).toBe(false)
    await wrapper.setProps({ loading: false, loadError: false })
    expect(wrapper.text()).toContain('admin.accounts.recentRequests.empty')
    expect(wrapper.text()).not.toContain('admin.accounts.recentRequests.loadFailed')
  })

  it('can render request indicators without opening a floating details panel', async () => {
    await wrapper.setProps({ interactive: false })
    await triggers()[1].trigger('mouseenter')
    await triggers()[1].trigger('click')
    expect(dialog()).toBeNull()
    expect(wrapper.find('[aria-haspopup="dialog"]').exists()).toBe(false)
  })
})

it.each([
  { request_type: 'sync', stream: false, cache_read_tokens: 0, expected: '-' },
  { request_type: 'sync', stream: true, cache_read_tokens: 800, expected: '-' },
  { request_type: 'stream', stream: true, cache_read_tokens: 0, expected: '0.00%' },
  { request_type: 'ws_v2', stream: false, cache_read_tokens: 800, expected: '80.00%' },
])('renders cache eligibility for $request_type', async ({ expected, ...request }) => {
  await wrapper.setProps({ requests: [{ ...success, ...request }] })
  await triggers()[0].trigger('mouseenter')
  expect(dialog()?.querySelector('[data-testid="recent-request-cache-rate"]')?.textContent).toBe(expected)
})
