import { describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import AdminCouponsView from '../AdminCouponsView.vue'
const api = vi.hoisted(() => ({ templates: vi.fn(), list: vi.fn(), issue: vi.fn() }))
vi.mock('@/api/coupons', () => ({ adminCouponAPI: api }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
describe('admin coupon issuance', () => {
 it('reuses the request identifier after uncertain delivery', async () => {
  api.templates.mockResolvedValue({ data: [{ id: 1, name: 'Coupon', enabled: true, kind: 'fixed', currency: 'CNY', min_amount: 100, value: 10, issued_count: 0 }] })
  api.list.mockResolvedValue({ data: { items: [], total: 0 } })
  api.issue.mockRejectedValueOnce(new Error('network timeout')).mockResolvedValueOnce({ data: { count: 2, ids: [1, 2] } })
  const w = shallowMount(AdminCouponsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } } });await flushPromises()
  const form = w.findAll('form')[0]
  await form.get('select').setValue('1');await form.findAll('input')[0].setValue('10 11');await form.trigger('submit');await flushPromises()
  const first = api.issue.mock.calls[0][0];expect(first.user_ids).toEqual([10,11])
  await form.trigger('submit');await flushPromises();expect(api.issue.mock.calls[1][0]).toEqual(first);expect(w.text()).toContain('coupon.issueSuccess');w.unmount()
 })
})
