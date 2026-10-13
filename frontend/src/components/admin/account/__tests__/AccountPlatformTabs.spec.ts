import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AccountPlatformTabs from '../AccountPlatformTabs.vue'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('AccountPlatformTabs', () => {
  it('shows only platforms with accounts, with global totals and names from the catalog', () => {
    const wrapper = mount(AccountPlatformTabs, {
      props: { modelValue: '', loading: false, error: false, counts: [
        { platform: 'deepseek', count: 3 }, { platform: 'openai', count: 1001 }, { platform: 'custom-provider', count: 2 }
      ] }
    })
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(4)
    expect(wrapper.get('[data-platform=""]').text()).toContain('1006')
    expect(wrapper.get('[data-platform="deepseek"]').text()).toContain('DeepSeek')
    expect(wrapper.get('[data-platform="custom-provider"]').text()).toContain('custom-provider')
    expect(wrapper.find('[data-platform="anthropic"]').exists()).toBe(false)
  })

  it('supports keyboard navigation and tab selection', async () => {
    const wrapper = mount(AccountPlatformTabs, {
      props: { modelValue: '', loading: false, error: false, counts: [{ platform: 'openai', count: 1 }, { platform: 'grok', count: 2 }] }
    })
    await wrapper.get('[data-platform=""]').trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['openai'])
    await wrapper.setProps({ modelValue: 'openai' })
    expect(wrapper.get('[data-platform="openai"]').attributes('tabindex')).toBe('0')
    expect(wrapper.get('[data-platform=""]').attributes('tabindex')).toBe('-1')
    await wrapper.get('[data-platform="openai"]').trigger('keydown', { key: 'End' })
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual(['grok'])
    await wrapper.get('[data-platform=""]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([''])
  })

  it('preserves the active tab at zero after the last account is deleted', async () => {
    const wrapper = mount(AccountPlatformTabs, {
      props: { modelValue: 'openai', loading: false, error: false, counts: [{ platform: 'openai', count: 1 }, { platform: 'grok', count: 2 }] }
    })
    await wrapper.setProps({ counts: [{ platform: 'grok', count: 2 }] })
    expect(wrapper.get('[data-platform="openai"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-platform="openai"]').text()).toContain('0')
    expect(wrapper.get('[data-platform=""]').text()).toContain('2')
    await wrapper.setProps({ modelValue: 'grok' })
    expect(wrapper.find('[data-platform="openai"]').exists()).toBe(false)
  })

  it('does not display a fabricated zero count before loading finishes', () => {
    const wrapper = mount(AccountPlatformTabs, { props: { modelValue: '', counts: null, loading: true, error: false } })
    expect(wrapper.get('[role="tablist"]').attributes('aria-busy')).toBe('true')
    expect(wrapper.get('[data-platform=""]').text()).toBe('admin.accounts.allPlatforms')
  })
})
