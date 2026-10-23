import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import { defaultPassivePolicy, type PassivePolicy } from '@/api/admin/passiveQuality'
import type { AccountListItem, AdminGroup } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'
import QualityPolicySummary from '../QualityPolicySummary.vue'

const render = (policy: PassivePolicy, policies: PassivePolicy[] = []) => mount(QualityPolicySummary, {
  props: { policy, policies, groups: [{ id: 8, name: 'Group A' }] as AdminGroup[], accounts: [{ id: 3, group_ids: [8, 9] }] as AccountListItem[] },
  global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] },
})

describe('policy behavior preview', () => {
  it('distinguishes observation, pause, limit and disabled behavior', async () => {
    const policy = { ...defaultPassivePolicy(), group_ids: [8] }
    const w = render(policy)
    expect(w.text()).toContain('No scheduling changes or recovery probes')
    expect(w.text()).not.toContain('Initial cooldown')
    await w.setProps({ policy: { ...policy, mode: 'enforce' } })
    expect(w.text()).toContain('Initial cooldown 120s')
    expect(w.text()).toContain('does not prove quality has recovered')
    await w.setProps({ policy: { ...policy, mode: 'enforce', action: 'limit' } })
    expect(w.text()).toContain('Concurrency cap 2')
    expect(w.text()).not.toContain('connectivity probe')
    await w.setProps({ policy: { ...policy, mode: 'enforce', enabled: false } })
    expect(w.text()).toContain('Policy is disabled')
    expect(w.text()).not.toContain('Initial cooldown')
    w.unmount()
  })

  it('shows inclusive boundaries and millisecond precision in seconds', () => {
    const w = render({ ...defaultPassivePolicy(), ttft: { enabled: true, pause: 10001, recover: 5001 }, cache_rate: { enabled: true, pause: 10, recover: 20 } })
    expect(w.text()).toContain('unhealthy ≥ 10.001; recover ≤ 5.001')
    expect(w.text()).toContain('unhealthy ≤ 10; recover ≥ 20')
    w.unmount()
  })

  it('detects overlapping account scope across groups while excluding itself and disabled policies', async () => {
    const policy = { ...defaultPassivePolicy(), id: 1, group_ids: [8], model: 'A', scope: 'model' as const }
    const other = { ...defaultPassivePolicy(), id: 2, name: 'Account wide', group_ids: [9], model: 'B' }
    const w = render(policy, [policy, other, { ...other, id: 3, name: 'Disabled', enabled: false }])
    expect(w.get('[data-policy-overlap]').text()).toContain('Account wide')
    expect(w.get('[data-policy-overlap]').text()).not.toContain('Disabled')
    await w.setProps({ policies: [{ ...other, scope: 'model' }] })
    expect(w.find('[data-policy-overlap]').exists()).toBe(false)
    await w.setProps({ policies: [{ ...other, excluded_account_ids: [3] }] })
    expect(w.find('[data-policy-overlap]').exists()).toBe(false)
    w.unmount()
  })
})
