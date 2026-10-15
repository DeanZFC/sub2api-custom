import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ProfileQQBotCard from '../ProfileQQBotCard.vue'

const api = vi.hoisted(() => ({ binding: vi.fn(), createCode: vi.fn(), confirm: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('QQ binding confirmation', () => {
  beforeEach(() => vi.resetAllMocks())
  it('requires a separate confirmation after the group claims a code', async () => {
    api.binding.mockResolvedValue({ enabled: true, binding: null, pending: null })
    api.createCode.mockResolvedValue({ code: 'single-use-code' })
    const wrapper = mount(ProfileQQBotCard)
    await flushPromises()
    await wrapper.findAll('button')[0].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('single-use-code')
    expect(api.confirm).not.toHaveBeenCalled()
    api.binding.mockResolvedValue({ enabled: true, binding: null, pending: { member_id: 'qq-member', nickname: 'Alice', group_id: 'allowed-group' } })
    await wrapper.findAll('button')[1].trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Alice')
    expect(api.confirm).not.toHaveBeenCalled()
    api.confirm.mockResolvedValue({ bound: true })
    api.binding.mockResolvedValue({ enabled: true, binding: { member_id: 'qq-member', nickname: 'Alice' }, pending: null })
    await wrapper.findAll('button')[2].trigger('click')
    await flushPromises()
    expect(api.confirm).toHaveBeenCalledWith('qq-member')
    expect(wrapper.text()).not.toContain('single-use-code')
    expect(wrapper.text()).toContain('qqbot.bound')
  })
  it('does not show binding controls when the bot is disabled', async () => {
    api.binding.mockResolvedValue({ enabled: false, binding: null, pending: null })
    const wrapper = mount(ProfileQQBotCard)
    await flushPromises()
    expect(wrapper.find('section').exists()).toBe(false)
  })
})
