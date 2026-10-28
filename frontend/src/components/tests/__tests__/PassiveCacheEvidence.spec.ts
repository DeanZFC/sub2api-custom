import { defaultPassiveCacheProtection } from '@/test/fixtures/passiveQuality'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import { type PassiveCacheState, type PassiveCacheWindowEvidence } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import PassiveCacheEvidence from '../PassiveCacheEvidence.vue'
const window: PassiveCacheWindowEvidence = { total_samples: 50, reported_samples: 42, reusable_samples: 30, cold_samples: 7, unknown_samples: 5, missing_telemetry_samples: 8, cache_rate: 0, protection_rate: null, read_tokens: 0, input_tokens: 90000, eligible_read_tokens: 0, eligible_input_tokens: 0, sessions: 1, max_session_share_percent: 100, fresh: 0, latest: '', complete: true, window_seconds: 900, max_samples: 200, reasons: { account_switched: 3 } }
const state: PassiveCacheState = { phase: 'reduced', reason: 'cache_pool_guard', hits: 3, quick_hits: 2, recovery_hits: 0, allocation_percent: 25, last_evidence_at: '', quick_evidence_at: '', observe_after: '', hold_until: '', updated_at: '', evidence: { model: 'model-a', quick: window, action: window } }
describe('cache reuse evidence', () => {
  it('distinguishes actual zero usage from unknown protection and exposes warmup/allocation', () => {
    const w = mount(PassiveCacheEvidence, { props: { cache: { 'model-a': state }, config: defaultPassiveCacheProtection() }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] } })
    expect(w.text()).toContain('Actual cache utilization: 0%')
    expect(w.text()).toContain('Reusable-request protection rate: —')
    expect(w.text()).toContain('New-session allocation 25%')
    expect(w.text()).toContain('Quick 2/2 · Reduction 3/3 · Recovery 0/2')
    expect(w.text()).toContain('Reusable 30 · Cold 7 · Unknown 5 · Missing cache telemetry 8')
    expect(w.text()).toContain('Pool cascade guard')
    expect(w.findAll('[data-evidence-window]')).toHaveLength(2)
    w.unmount()
  })
  it('shows recorded request impact without treating missing cost or latency as zero', async () => {
    const render = (evidence: PassiveCacheWindowEvidence) => mount(PassiveCacheEvidence, { props: { cache: { 'model-a': { ...state, evidence: { model: 'model-a', quick: evidence, action: evidence } } } }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] } })
    const w = render(window)
    expect(w.get('[data-cache-impact]').text()).toContain('Account switches — · TTFT P90 — (— samples) · Input and cache priced cost (USD) — (—/50 records)')
    expect(w.get('[data-cache-impact]').text()).toContain('not estimated savings')
    w.unmount()
    const recorded = render({ ...window, account_switches: 3, ttft_p90: 1234.56, ttft_samples: 12, input_cost: 0.001234, input_cost_samples: 42 })
    expect(recorded.get('[data-cache-impact]').text()).toContain('Account switches 3 · TTFT P90 1234.56 ms (12 samples) · Input and cache priced cost (USD) $0.001234 (42/50 records)')
    expect(recorded.text()).toContain('Successful relevant requests 50')
    recorded.unmount()
  })

})
