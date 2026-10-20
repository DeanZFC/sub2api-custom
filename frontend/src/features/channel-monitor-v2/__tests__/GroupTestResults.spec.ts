import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import GroupTestFooter from '../GroupTestFooter.vue'
import GroupPelicanGallery from '../GroupPelicanGallery.vue'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { TestResult } from '@/types'

const pelicans: TestResult[] = Array.from({ length: 5 }, (_, index) => ({
  id: index + 1, account_id: index + 10, model_id: 'model-' + index,
  status: index === 4 ? 'error' : 'success', output_kind: 'html', output_html: index === 4 ? undefined : '<svg />',
  account_schedulable: index !== 4,
}))
const globals = () => ({
  plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })],
  stubs: { TestResultOutput: { props: ['result'], template: '<div class="output">{{ result.id }}</div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' } },
})

describe('group test display', () => {
  it('caps only card thumbnails and opens the full gallery even if all results failed', async () => {
    const wrapper = mount(GroupTestFooter, { props: { results: { candy: [], pelican: pelicans }, state: 'ready', groupName: '渠道 A' }, global: globals() })
    expect(wrapper.findAll('[data-testid=pelican-thumbnail]')).toHaveLength(3)
    expect(wrapper.text()).toContain('5 账号')
    expect(wrapper.get('[data-testid=candy-summary]').text()).toBe('暂无结果')
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('open')).toHaveLength(1)
    await wrapper.setProps({ results: { candy: [], pelican: [pelicans[4]] } })
    expect(wrapper.findAll('[data-testid=pelican-thumbnail]')).toHaveLength(0)
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    await wrapper.setProps({ state: 'error' })
    expect(wrapper.text()).toContain('加载失败')
    expect(wrapper.get('button').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('shows every account including paused failures, supports enlarging, and resets on close', async () => {
    const wrapper = mount(GroupPelicanGallery, { props: { results: pelicans, show: true, groupName: '渠道 A' }, global: globals() })
    expect(wrapper.findAll('[data-testid=pelican-account]')).toHaveLength(5)
    expect(wrapper.findAll('[data-testid=pelican-result]')).toHaveLength(5)
    expect(wrapper.text()).toContain('账号 #14')
    expect(wrapper.text()).toContain('暂停调度')
    expect(wrapper.text()).toContain('失败')
    await wrapper.findAll('[data-testid=pelican-enlarge]')[3].trigger('click')
    expect(wrapper.get('[data-testid=pelican-expanded]').text()).toContain('账号 #13')
    expect(wrapper.get('[data-testid=pelican-expanded]').text()).toContain('model-3')
    await wrapper.get('[data-testid=pelican-back]').trigger('click')
    expect(wrapper.findAll('[data-testid=pelican-account]')).toHaveLength(5)
    await wrapper.findAll('[data-testid=pelican-enlarge]')[0].trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    expect(wrapper.findAll('[data-testid=pelican-account]')).toHaveLength(5)
    wrapper.unmount()
  })
})
