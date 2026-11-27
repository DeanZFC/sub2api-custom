import { flushPromises, mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import GroupProbePanel from '../GroupProbePanel.vue'

const api = vi.hoisted(() => ({
  listGroupProbes: vi.fn(), runGroupProbe: vi.fn(), saveGroupProbe: vi.fn(),
  getAll: vi.fn(), getModelAllowlistCandidates: vi.fn(),
}))
vi.mock('@/api/admin/groupProbes', () => api)
vi.mock('@/api/admin/groups', () => api)
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))

it('saves a group model and retains the completed result while a manual probe is pending', async () => {
  const probe = { group_id: 7, group_name: 'Private group', model: 'public-model', enabled: true, pending: false,
    idle_window_minutes: 15, min_requests: 3, duration_minutes: 12, interval_seconds: 17, round_until: '2026-01-01T12:12:00Z',
    last_probe_at: '2026-01-01T12:00:00Z', last_user_at: null, status: 'healthy',
    result: { first_token_ms: 851, estimated_cost_usd: null, tokens: null } }
  api.getAll.mockResolvedValue([
    { id: 7, name: 'Private group', platform: 'openai' },
    { id: 8, name: 'Second group', platform: 'openai' },
  ])
  api.getModelAllowlistCandidates.mockResolvedValue(['public-model'])
  api.listGroupProbes.mockResolvedValueOnce([]).mockResolvedValueOnce([probe])
    .mockResolvedValueOnce([{ ...probe, enabled: false }]).mockResolvedValueOnce([probe])
    .mockResolvedValue([{ ...probe, pending: true }])
  const view = mount(GroupProbePanel)
  try {
    await flushPromises()
    await view.get('select').setValue(7)
    await flushPromises()
    expect(view.get('datalist option').attributes('value')).toBe('public-model')
    const minutes = view.get<HTMLInputElement>('input[name="idle_window_minutes"]')
    const requests = view.get<HTMLInputElement>('input[name="min_requests"]')
    const duration = view.get<HTMLInputElement>('input[name="duration_minutes"]')
    const interval = view.get<HTMLInputElement>('input[name="interval_seconds"]')
    await view.get('input[list]').setValue('public-model')
    expect([minutes, requests, duration, interval].map(input => input.element.value)).toEqual(['5', '5', '10', '30'])
    await minutes.setValue('1441')
    expect(view.get('form button').attributes('disabled')).toBeDefined()
    await minutes.setValue('15')
    await requests.setValue('0')
    expect(view.get('form button').attributes('disabled')).toBeDefined()
    await requests.setValue('3')
    await duration.setValue('0')
    expect(view.get('form button').attributes('disabled')).toBeDefined()
    await duration.setValue('12')
    await interval.setValue('1.5')
    expect(view.get('form button').attributes('disabled')).toBeDefined()
    await interval.setValue('17')
    expect(view.get('form button').attributes('disabled')).toBeUndefined()
    await view.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveGroupProbe).toHaveBeenCalledWith(7, 'public-model', true, { idle_window_minutes: 15, min_requests: 3, duration_minutes: 12, interval_seconds: 17 })
    expect(view.get('tbody').text()).toContain('channelMonitorV2.probes.roundUntil')
    await view.get('select').setValue(8)
    expect([minutes, requests, duration, interval].map(input => input.element.value)).toEqual(['5', '5', '10', '30'])
    await view.get('select').setValue(7)
    expect([minutes, requests, duration, interval].map(input => input.element.value)).toEqual(['15', '3', '12', '17'])
    const toggle = view.get('tbody button:last-child')
    await toggle.trigger('click')
    await flushPromises()
    expect(api.saveGroupProbe).toHaveBeenLastCalledWith(7, 'public-model', false)
    await toggle.trigger('click')
    await flushPromises()
    expect(api.saveGroupProbe).toHaveBeenLastCalledWith(7, 'public-model', true)
    await view.get('tbody button').trigger('click')
    await flushPromises()
    expect(api.runGroupProbe).toHaveBeenCalledWith(7)
    expect(view.get('tbody button').attributes('disabled')).toBeDefined()
    expect(view.get('tbody').text()).toContain('851 ms')
    expect(view.get('tbody').text()).toContain('channelMonitorV2.probes.pending')
  } finally {
    view.unmount()
  }
})
