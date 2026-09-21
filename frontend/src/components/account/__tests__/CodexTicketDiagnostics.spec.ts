import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import CodexTicketDiagnostics from '../CodexTicketDiagnostics.vue'
import type { CodexTurnTicketStatus } from '@/types'

vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))
vi.mock('vue-i18n', async () => {
  const { default: messages } = await import('@/i18n/locales/zh/admin/accounts')
  return { useI18n: () => ({
    t: (key: string, params: Record<string, unknown> = {}) => {
      const value = key.replace(/^admin\./, '').split('.').reduce<unknown>(
        (current, segment) => (current as Record<string, unknown>)[segment], messages,
      )
      return String(value).replace(/\{(\w+)\}/g, (_, name: string) => String(params[name]))
    },
  }) }
})

const ticket: CodexTurnTicketStatus = {
  model: 'gpt-6-astra', target_length: 332, ready: false, remaining_seconds: 0, blocked: true,
  attempts: 12, consecutive_failures: 4, last_attempt_at: '2026-09-19T10:00:00Z',
  next_retry_at: '2026-09-19T10:00:06Z', last_error_code: 'length_mismatch',
  last_error: 'https://user:secret@proxy.invalid/private-token',
  last_length: 0, last_http_status: 200, last_proxy_index: 2, plan_known: false,
}

function mountDiagnostics(overrides: Partial<CodexTurnTicketStatus> = {}, compact = false) {
  return mount(CodexTicketDiagnostics, {
    props: { ticket: { ...ticket, ...overrides }, compact },
  })
}

describe('CodexTicketDiagnostics', () => {
  it('shows safe failure details, zero returned length, retry time and unknown plan', () => {
    const wrapper = mountDiagnostics()
    expect(wrapper.text()).toContain('返回门票长度与账号套餐不符')
    expect(wrapper.text()).toContain('已尝试 12 次')
    expect(wrapper.text()).toContain('连续失败：4 次')
    expect(wrapper.text()).toContain('返回 / 目标长度：0 / 332')
    expect(wrapper.text()).toContain('下次重试：2026-09-19T10:00:06Z')
    expect(wrapper.text()).toContain('Team 应为 332')
    expect(wrapper.text()).toContain('池 #2')
    expect(wrapper.text()).toContain('仅记录本次服务运行，重启或停用打票后清空')
    expect(wrapper.html()).not.toContain('secret')
    expect(wrapper.html()).not.toContain('private-token')
  })

  it('keeps list output compact with full diagnostics on hover', () => {
    const wrapper = mountDiagnostics({}, true)
    expect(wrapper.text()).toBe('返回门票长度与账号套餐不符 · 已尝试 12 次')
    expect(wrapper.attributes('title')).toContain('返回 / 目标长度：0 / 332')
    expect(wrapper.attributes('title')).toContain('下次重试：2026-09-19T10:00:06Z')
    expect(wrapper.attributes('title')).not.toContain('secret')
  })

  it('distinguishes in-progress and paused harvesting from model blocking', async () => {
    const wrapper = mountDiagnostics({ in_progress: true, plan_known: true })
    expect(wrapper.text()).toContain('正在打票')
    expect(wrapper.text()).not.toContain('下次重试')
    expect(wrapper.text()).not.toContain('自动重试已暂停')
    await wrapper.setProps({ ticket: { ...ticket, paused: true, in_progress: false } })
    expect(wrapper.text()).toContain('自动重试已暂停')
    expect(wrapper.text()).not.toContain('下次重试')
    expect(wrapper.text()).toContain('恢复检查时间：2026-09-19T10:00:06Z')
    expect(wrapper.attributes('title')).toContain('恢复检查时间：2026-09-19T10:00:06Z')
  })

  it('uses renewal labels for cached tickets and safe fallback for unknown error codes', () => {
    const wrapper = mountDiagnostics({ ready: true, last_error_code: 'unexpected_secret', plan_known: true })
    expect(wrapper.text()).toContain('下次续票：2026-09-19T10:00:06Z')
    expect(wrapper.text()).toContain('打票失败，请检查服务日志')
    expect(wrapper.html()).not.toContain('unexpected_secret')
  })

  it('shows process counters and the last injection miss with a compact list summary', () => {
    const overrides = { successes: 8, failures: 4, inject_misses: 2, last_inject_miss_at: '2026-09-19T11:00:00Z' }
    const wrapper = mountDiagnostics(overrides)
    expect(wrapper.text()).toContain('累计成功：8 次')
    expect(wrapper.text()).toContain('累计失败：4 次')
    expect(wrapper.text()).toContain('注入缺失：2 次')
    expect(wrapper.text()).toContain('最近注入缺失：2026-09-19T11:00:00Z')
    const compact = mountDiagnostics(overrides, true)
    expect(compact.text()).toContain('成功 8 / 失败 4 / 缺失 2')
    expect(compact.attributes('title')).toContain('最近注入缺失：2026-09-19T11:00:00Z')
  })

  it.each([
    ['account', '账号代理'],
    ['direct', '直连'],
    ['pool', '池 #2'],
  ] as const)('shows the safe %s route label without exposing a URL', (source, label) => {
    const wrapper = mountDiagnostics({ last_proxy_source: source, last_proxy_index: 2 })
    expect(wrapper.text()).toContain(label)
    if (source !== 'pool') expect(wrapper.text()).not.toContain('池 #2')
    expect(wrapper.html()).not.toContain('https://')
  })
})
