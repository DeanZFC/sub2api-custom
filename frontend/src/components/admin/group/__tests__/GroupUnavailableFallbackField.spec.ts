import { defineComponent } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupUnavailableFallbackField from '../GroupUnavailableFallbackField.vue'

const { getAll } = vi.hoisted(() => ({ getAll: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getAll } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const Select = defineComponent({ props: ['options', 'modelValue', 'disabled'], emits: ['update:modelValue'], template: '<div />' })
beforeEach(() => {
  getAll.mockReset()
  getAll.mockResolvedValue([
    { id: 1, name: 'Primary', platform: 'openai', status: 'active' },
    { id: 2, name: 'Pool outside current page', platform: 'openai', status: 'active' },
    { id: 3, name: 'Other platform', platform: 'anthropic', status: 'active' },
    { id: 4, name: 'Disabled', platform: 'openai', status: 'inactive' }
  ])
})
describe('GroupUnavailableFallbackField', () => {
  it('loads all groups, filters by platform and self, and allows clearing', async () => {
    const wrapper = mount(GroupUnavailableFallbackField, { props: { platform: 'openai', groupId: 1, modelValue: 2 }, global: { stubs: { Select } } })
    await flushPromises()
    const select = wrapper.findComponent(Select)
    expect(select.props('options')).toEqual([
      { value: null, label: 'admin.groups.unavailableFallback.none' },
      { value: 2, label: 'Pool outside current page' }
    ])
    select.vm.$emit('update:modelValue', null)
    expect(wrapper.emitted('update:modelValue')).toEqual([[null]])
    wrapper.unmount()
  })
  it('preserves an existing target during a fetch failure and permits retry', async () => {
    getAll.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(GroupUnavailableFallbackField, { props: { platform: 'openai', groupId: 1, modelValue: 2 }, global: { stubs: { Select } } })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.groups.unavailableFallback.loadFailed')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.findComponent(Select).props('options')).toContainEqual({ value: 2, label: 'Pool outside current page' })
    wrapper.unmount()
  })
})
