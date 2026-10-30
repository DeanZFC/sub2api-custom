import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import StrictFailoverFields from '../StrictFailoverFields.vue'
import type { StrictFailoverConfig } from '@/types'

const { list } = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { list } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: { count?: number }) => params?.count === undefined ? key : `${key}: ${params.count}` }) }))
const Toggle = defineComponent({ props: ['modelValue'], emits: ['update:modelValue'], setup: (props, { emit }) => () => h('button', { onClick: () => emit('update:modelValue', !props.modelValue) }, 'toggle') })
function render(config: StrictFailoverConfig = { enabled: false, tiers: [] }) {
  const wrapper = mount(StrictFailoverFields, { props: { groupId: 75, modelValue: config }, global: { stubs: { Toggle } } })
  return wrapper
}
beforeEach(() => { list.mockReset(); list.mockResolvedValue({ items: [{ id: 1, name: 'Primary A' }, { id: 2, name: 'Primary B' }, { id: 3, name: 'Backup' }], total: 3 }) })

describe('StrictFailoverFields', () => {
  it('keeps existing groups disabled and loads only on enable', async () => {
    const wrapper = render()
    expect(list).not.toHaveBeenCalled()
    await wrapper.findComponent(Toggle).trigger('click')
    const value = wrapper.emitted('update:modelValue')![0]![0] as StrictFailoverConfig
    expect(value).toEqual({ enabled: true, tiers: [[]] })
    await wrapper.setProps({ modelValue: value }); await flushPromises()
    expect(list).toHaveBeenCalledWith(1, 100, { group: '75' }, { signal: expect.any(AbortSignal) })
  })
  it('shows existing tiers and prevents assigning one account twice', async () => {
    const wrapper = render({ enabled: true, tiers: [[1, 2], [3]] }); await flushPromises()
    expect(wrapper.findAll('[data-testid="strict-tier"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('Primary A')
    await wrapper.find('input').trigger('focus')
    expect(wrapper.text()).toContain('admin.groups.strictFailover.noResults')
    expect((wrapper.vm as unknown as { validate: () => boolean }).validate()).toBe(true)
  })
  it('keeps unassigned group accounts in an automatic final tier as assignments change', async () => {
    const wrapper = render({ enabled: true, tiers: [[1, 2]] }); await flushPromises()
    expect(wrapper.find('[data-testid="strict-automatic-tier"]').text()).toContain('admin.groups.strictFailover.automaticTier')
    expect(wrapper.find('[data-testid="strict-automatic-count"]').text()).toBe('admin.groups.strictFailover.automaticCount: 1')
    await wrapper.find('button[aria-label="admin.groups.strictFailover.removeAccount 2"]').trigger('click')
    const removed = wrapper.emitted('update:modelValue')!.at(-1)![0] as StrictFailoverConfig
    expect(removed).toEqual({ enabled: true, tiers: [[1]] })
    await wrapper.setProps({ modelValue: removed })
    expect(wrapper.find('[data-testid="strict-automatic-count"]').text()).toBe('admin.groups.strictFailover.automaticCount: 2')
    await wrapper.find('input').trigger('focus')
    await wrapper.findAll('button').find(button => button.text() === 'Backup · #3')!.trigger('click')
    const assigned = wrapper.emitted('update:modelValue')!.at(-1)![0] as StrictFailoverConfig
    expect(assigned).toEqual({ enabled: true, tiers: [[1, 3]] })
    await wrapper.setProps({ modelValue: assigned })
    expect(wrapper.find('[data-testid="strict-automatic-count"]').text()).toBe('admin.groups.strictFailover.automaticCount: 1')
    expect(wrapper.findAll('[data-testid="strict-tier"]')).toHaveLength(1)
  })
  it('rejects empty and duplicated tiers without losing the entered configuration', async () => {
    const cfg = { enabled: true, tiers: [[1], []] }
    const wrapper = render(cfg); await flushPromises()
    expect((wrapper.vm as unknown as { validate: () => boolean }).validate()).toBe(false)
    await nextTick(); expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    await wrapper.setProps({ modelValue: { enabled: true, tiers: [[1], [1]] } })
    expect((wrapper.vm as unknown as { validate: () => boolean }).validate()).toBe(false)
    expect(cfg.tiers).toEqual([[1], []])
  })
  it('reorders tiers and preserves configuration when disabled', async () => {
    const cfg = { enabled: true, tiers: [[1], [2, 3]] }
    const wrapper = render(cfg); await flushPromises()
    const tiers = wrapper.findAll('[data-testid="strict-tier"]')
    await tiers[1]!.find('button').trigger('click')
    expect(wrapper.emitted('update:modelValue')![0]![0]).toEqual({ enabled: true, tiers: [[2, 3], [1]] })
    expect(cfg.tiers).toEqual([[1], [2, 3]])
    await wrapper.findComponent(Toggle).trigger('click')
    expect(wrapper.emitted('update:modelValue')!.at(-1)![0]).toEqual({ enabled: false, tiers: [[1], [2, 3]] })
  })
  it('loads subsequent pages and makes fetch failures visible', async () => {
    list.mockResolvedValueOnce({ items: [{ id: 1, name: 'first' }], total: 2 }).mockResolvedValueOnce({ items: [{ id: 3, name: 'last' }], total: 2 })
    const wrapper = render({ enabled: true, tiers: [[1], [3]] }); await flushPromises()
    expect(list).toHaveBeenCalledTimes(2); expect(wrapper.text()).toContain('last')
    list.mockRejectedValueOnce(new Error('offline'))
    await wrapper.setProps({ groupId: 76 }); await flushPromises()
    expect(wrapper.text()).toContain('admin.groups.strictFailover.loadError')
    expect(wrapper.find('[data-testid="strict-automatic-count"]').exists()).toBe(false)
    await wrapper.setProps({ groupId: 75 }); await flushPromises()
    expect(list).toHaveBeenCalledTimes(4)
    expect(wrapper.text()).toContain('Primary A')
  })
  it('counts accounts from every page only after the complete group load succeeds', async () => {
    let resolveLastPage!: (value: { items: Array<{ id: number; name: string }>; total: number }) => void
    list.mockResolvedValueOnce({ items: [{ id: 1, name: 'first' }], total: 3 })
      .mockReturnValueOnce(new Promise(resolve => { resolveLastPage = resolve }))
    const wrapper = render({ enabled: true, tiers: [[1]] }); await flushPromises()
    expect(wrapper.find('[data-testid="strict-automatic-count"]').exists()).toBe(false)
    resolveLastPage({ items: [{ id: 2, name: 'last A' }, { id: 3, name: 'last B' }], total: 3 })
    await flushPromises()
    expect(wrapper.find('[data-testid="strict-automatic-count"]').text()).toBe('admin.groups.strictFailover.automaticCount: 2')
  })
})
