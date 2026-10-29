import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import { useQualityLabels } from '../useQualityLabels'
import resources from '@/i18n/locales/en/admin/resources'
import zhResources from '@/i18n/locales/zh/admin/resources'

const renderReason = (reason: string, locale = 'en') => mount(defineComponent({
  setup() { const labels = useQualityLabels(); return () => h('p', labels.reason(reason)) },
}), { global: { plugins: [createI18n({ legacy: false, locale, missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources }, zh: { admin: zhResources } } })] } })

describe('quality reason labels', () => {
  it('translates nested active failures and each independent metric reason', () => {
    const w = renderReason('active_recovery_failed: cache_rate: 50.00 / 85.00 (5); output_tps: 12.00 / 36.00 (5)')
    expect(w.text()).toContain('Active recovery failed; protection remains')
    expect(w.text()).toContain('Cache usage (%)')
    expect(w.text()).toContain('Output TPS')
    expect(w.text()).not.toContain('active_recovery_failed')
    expect(w.text()).not.toContain('cache_rate:')
    w.unmount()
  })

  it('explains initial validation and interrupted recovery without customer trials', () => {
    const initial = renderReason('active_recovery_running: initial_verification', 'zh')
    expect(initial.text()).toContain('先执行主动验证')
    expect(initial.text()).not.toContain('initial_verification')
    initial.unmount()
    const interrupted = renderReason('active_recovery_pending: probe_interrupted')
    expect(interrupted.text()).toContain('lease expired')
    expect(interrupted.text()).not.toContain('probe_interrupted')
    interrupted.unmount()
  })

  it('explains the fast failure window and retains unknown active reason details', () => {
    const burst = renderReason('consecutive_failures: 3 within 60s', 'zh')
    expect(burst.text()).toBe('60 秒内连续上游失败 3 次')
    burst.unmount()
    const unknown = renderReason('active_recovery_new_condition')
    expect(unknown.text()).toBe('Active recovery is incomplete: active_recovery_new_condition')
    unknown.unmount()
  })

  it('explains unsupported recovery metrics without implying a paid test or recovery', () => {
    const english = renderReason('active_recovery_pending: active_probe_metrics_unsupported: Bedrock')
    expect(english.text()).toContain('no upstream requests are sent')
    expect(english.text()).toContain('protection remains pending')
    expect(english.text()).toContain('Bedrock')
    expect(english.text()).not.toContain('active_probe_metrics_unsupported')
    english.unmount()
    const chinese = renderReason('active_probe_metrics_unsupported', 'zh')
    expect(chinese.text()).toContain('本轮不发送上游请求')
    expect(chinese.text()).toContain('保持暂停并等待')
    chinese.unmount()
  })

  it('translates each channel mapping wait reason in both languages without reporting recovery', () => {
    const cases = [
      ['active_recovery_group_ambiguous', 'cannot be identified uniquely', '无法唯一确定'],
      ['active_recovery_channel_mapping_unavailable', 'mappings are temporarily unavailable', '映射暂时无法读取'],
      ['active_recovery_channel_model_unavailable', 'has no usable recovery model', '没有可用的恢复模型'],
      ['active_recovery_channel_model_restricted', 'restricted by the channel', '受渠道规则限制'],
    ]
    for (const [key, englishText, chineseText] of cases) {
      for (const [locale, expected, pending] of [['en', englishText, 'protection remains pending'], ['zh', chineseText, '保持暂停并等待']]) {
        const wrapper = renderReason(`active_recovery_pending: ${key}`, locale)
        expect(wrapper.text()).toContain(expected)
        expect(wrapper.text()).toContain(pending)
        expect(wrapper.text()).not.toContain(key)
        expect(wrapper.text()).not.toContain('active_recovery_pending')
        wrapper.unmount()
      }
    }
  })
})
