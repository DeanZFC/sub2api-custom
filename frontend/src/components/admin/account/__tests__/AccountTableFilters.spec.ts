import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import AccountTableFilters from '../AccountTableFilters.vue'
import Select from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import type { AdminGroup } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { count: number }) => params ? `${key} (${params.count})` : key
    })
  }
})

const defaultFilters = () => ({
  platform: '', type: '', status: '', privacy_mode: '', group: '',
  search: 'existing search', lite: '1', sort_by: 'priority', sort_order: 'asc'
})

function mountFilters(filters: Record<string, unknown> = defaultFilters()) {
  return mount(AccountTableFilters, {
    attachTo: document.body,
    props: {
      searchQuery: 'existing search', filters,
      groups: [{ id: 42, name: 'A very long group name' } as AdminGroup]
    },
    global: { stubs: { Teleport: true } }
  })
}

enableAutoUnmount(afterEach)
afterEach(() => {
  vi.useRealTimers()
  document.body.innerHTML = ''
})

describe('AccountTableFilters', () => {
  it('shows every filter directly and can hide the platform selector for platform tabs', async () => {
    const wrapper = mountFilters()
    expect(wrapper.findAllComponents(Select)).toHaveLength(5)
    expect(wrapper.findAllComponents(Select).every(select => select.isVisible())).toBe(true)
    await wrapper.setProps({ hidePlatform: true })
    expect(wrapper.findAllComponents(Select)).toHaveLength(4)
    expect(wrapper.findAllComponents(Select).map(select => select.attributes('aria-label')))
      .not.toContain('admin.accounts.allPlatforms')
    expect(wrapper.emitted('update:filters')).toBeUndefined()
  })

  it('preserves every option and emits a merged filter snapshot plus change for every selection', async () => {
    const filters = { ...defaultFilters(), platform: 'openai', type: 'oauth', status: 'active', privacy_mode: '__unset__', group: '42' }
    const wrapper = mountFilters(filters)
    const expectedOptions = [
      ['platform', ['', ...CONCRETE_PLATFORM_OPTIONS.map(option => option.value)]],
      ['type', ['', 'oauth', 'setup-token', 'apikey', 'bedrock']],
      ['status', ['', 'active', 'inactive', 'error', 'quality_paused', 'rate_limited', 'temp_unschedulable', 'unschedulable']],
      ['privacy_mode', ['', '__unset__', 'training_off', 'training_set_cf_blocked', 'training_set_failed']],
      ['group', ['', 'ungrouped', '42']]
    ] as const
    let changes = 0
    for (const [index, [key, values]] of expectedOptions.entries()) {
      const select = wrapper.findAllComponents(Select)[index]
      expect(select.props('options').map(option => option.value)).toEqual(values)
      expect(select.props('modelValue')).toBe(filters[key])
      expect(select.get('button').attributes('aria-label')).not.toBe('Select option')
      for (const [optionIndex, value] of values.entries()) {
        await select.get('button').trigger('click')
        await select.findAll('[role="option"]')[optionIndex].trigger('click')
        changes += 1
        expect(wrapper.emitted('update:filters')?.at(-1)).toEqual([{ ...filters, [key]: value }])
        expect(wrapper.emitted('change')).toHaveLength(changes)
        expect(wrapper.props('filters')).toEqual(filters)
      }
    }
    expect(wrapper.findAllComponents(Select)[4].props('options')[2].label).toBe('A very long group name')
    wrapper.unmount()
  })

  it('keeps the custom quality-paused status selectable without clearing other filters', async () => {
    const filters = { ...defaultFilters(), platform: 'openai', type: 'oauth', privacy_mode: '__unset__', group: '42' }
    const wrapper = mountFilters(filters)
    const status = wrapper.findAllComponents(Select)[2]
    await status.get('button').trigger('click')
    const qualityPaused = status.findAll('[role="option"]').find(option =>
      option.text() === 'admin.accounts.status.qualityPaused'
    )
    expect(qualityPaused).toBeDefined()

    await qualityPaused!.trigger('click')

    expect(wrapper.emitted('update:filters')).toEqual([[{ ...filters, status: 'quality_paused' }]])
    expect(wrapper.emitted('change')).toEqual([[]])
    expect(wrapper.props('filters')).toEqual(filters)
  })

  it('preserves immediate search updates and the debounced search change event', async () => {
    vi.useFakeTimers()
    const wrapper = mountFilters()
    const search = wrapper.getComponent(SearchInput)
    expect(search.props('modelValue')).toBe('existing search')
    expect(search.props('placeholder')).toBe('admin.accounts.searchAccounts')
    await search.get('input').setValue('new search')
    expect(wrapper.emitted('update:searchQuery')).toEqual([['new search']])
    expect(wrapper.emitted('change')).toBeUndefined()
    await vi.advanceTimersByTimeAsync(300)
    expect(wrapper.emitted('change')).toEqual([[]])
    expect(wrapper.emitted('update:filters')).toBeUndefined()
    wrapper.unmount()
  })

})
