import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import CallErrorsView from '../CallErrorsView.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import type { OpsErrorLog, OpsErrorLogsResponse } from '@/api/admin/ops'

const { listErrorLogs, searchUsers, searchApiKeys, listGroups, listAccounts } = vi.hoisted(() => ({
  listErrorLogs: vi.fn(),
  searchUsers: vi.fn(),
  searchApiKeys: vi.fn(),
  listGroups: vi.fn(),
  listAccounts: vi.fn(),
}))

vi.mock('@/api/admin/ops', () => ({ listErrorLogs }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: { searchUsers, searchApiKeys },
    groups: { list: listGroups },
    accounts: { list: listAccounts },
  },
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const SelectStub = defineComponent({
  props: ['modelValue', 'options'],
  emits: ['update:modelValue', 'change'],
  setup(props, { emit }) {
    return {
      change(event: Event) {
        const value = (event.target as HTMLSelectElement).value
        const option = props.options.find((item: { value: unknown }) => String(item.value ?? '') === value)
        emit('update:modelValue', option?.value)
        emit('change', option?.value)
      },
    }
  },
  template: `<select :aria-label="options[0]?.label" :value="modelValue ?? ''" @change="change">
    <option v-for="option in options" :key="String(option.value)" :value="option.value ?? ''">{{ option.label }}</option>
  </select>`,
})

function mountPage({ realSelect = false } = {}) {
  return mount(CallErrorsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        ...(realSelect ? {} : { Select: SelectStub }),
        DateRangePicker: {
          name: 'DateRangePicker',
          props: ['startDate', 'endDate'],
          emits: ['update:startDate', 'update:endDate', 'change'],
          template: '<div />',
        },
        OpsErrorLogTable: {
          name: 'OpsErrorLogTable',
          props: ['rows', 'total', 'loading', 'page', 'pageSize'],
          emits: ['openErrorDetail', 'sort', 'update:page', 'update:pageSize'],
          template: '<div data-test="error-table"><span v-for="row in rows" :key="row.id">{{ row.message }}</span></div>',
        },
        OpsErrorDetailModal: {
          name: 'OpsErrorDetailModal',
          props: ['show', 'errorId', 'errorType'],
          emits: ['update:show'],
          template: '<div v-if="show" data-test="error-detail">{{ errorId }}</div>',
        },
      },
    },
  })
}

function response(id?: number): OpsErrorLogsResponse {
  const items = id == null ? [] : [{ id, message: `Failure ${id}`, created_at: '2026-10-16T01:00:00Z' } as OpsErrorLog]
  return { items, total: items.length, page: 1, page_size: 20, pages: items.length }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

async function chooseUser(wrapper: ReturnType<typeof mountPage>) {
  const input = wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]')
  await input.trigger('focus')
  await input.setValue('alice')
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
  const button = wrapper.findAll('button').find((item) => item.text().includes('alice@example.test'))
  expect(button).toBeDefined()
  await button!.trigger('click')
  await flushPromises()
}

enableAutoUnmount(afterEach)

describe('admin CallErrorsView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 9, 16, 12))
    listErrorLogs.mockReset().mockResolvedValue(response())
    searchUsers.mockReset().mockResolvedValue([{ id: 7, email: 'alice@example.test', deleted: false }])
    searchApiKeys.mockReset().mockResolvedValue([{ id: 11, name: 'QQ bot' }])
    listGroups.mockReset().mockResolvedValue({ items: [{ id: 3, name: 'Image API' }] })
    listAccounts.mockReset().mockResolvedValue({ items: [] })
  })

  afterEach(() => { vi.useRealTimers() })

  it('loads all failure categories for the last seven local calendar days, with an exclusive end boundary', async () => {
    const wrapper = mountPage()
    await flushPromises()
    expect(listErrorLogs).toHaveBeenCalledTimes(1)
    expect(listErrorLogs).toHaveBeenCalledWith(expect.objectContaining({
      view: 'all', page: 1, page_size: 20, sort_by: 'created_at', sort_order: 'desc',
      start_time: new Date(2026, 9, 10).toISOString(),
      end_time: new Date(2026, 9, 17).toISOString(),
    }))
    expect(wrapper.findComponent(UsageFilters).props()).toMatchObject({ mode: 'errors', showModelFilter: false })
    expect(wrapper.text()).not.toContain('usage.exportExcel')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

  it('combines website user, API key, group, phase, category and status filters from the controls', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await chooseUser(wrapper)
    expect(searchApiKeys).toHaveBeenCalledWith(7, '')
    await wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]').trigger('focus')
    await wrapper.findAll('button').find((button) => button.text().includes('QQ bot'))!.trigger('click')
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('3')
    await wrapper.get('select[aria-label="admin.usage.allTypes"]').setValue('upstream')
    await wrapper.get('select[aria-label="usage.errors.allCategories"]').setValue('upstream')
    await wrapper.get('select[aria-label="usage.errors.allStatuses"]').setValue('503')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({
      user_id: 7, api_key_id: 11, group_id: 3, phase: 'upstream', category: 'upstream', status_codes: '503',
    }))
  })

  it('displays All for unfiltered dropdowns in the real Select component, including after reset', async () => {
    const wrapper = mountPage({ realSelect: true })
    await flushPromises()
    const labels = () => wrapper.findAll('.select-value').map((item) => item.text())
    const expected = ['admin.usage.allTypes', 'usage.errors.allCategories', 'usage.errors.allStatuses', 'admin.usage.allGroups']
    expect(labels()).toEqual(expected)
    wrapper.findComponent(UsageFilters).props('modelValue').group_id = 3
    await flushPromises()
    expect(labels()).toContain('Image API')
    await wrapper.findAll('button').find((button) => button.text() === 'common.reset')!.trigger('click')
    await flushPromises()
    expect(labels()).toEqual(expected)
  })

  it('searches keys without requiring a user selection', async () => {
    const wrapper = mountPage()
    await flushPromises()
    const input = wrapper.get('input[placeholder="admin.usage.searchApiKeyPlaceholder"]')
    await input.trigger('focus')
    await input.setValue('QQ')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(searchApiKeys).toHaveBeenCalledWith(undefined, 'QQ')
    await wrapper.findAll('button').find((button) => button.text().includes('QQ bot'))!.trigger('click')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({ api_key_id: 11, user_id: undefined }))
  })

  it('preserves filters through pagination and sorting, and resets the page when filters or page size change', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('3')
    await wrapper.get('#call-error-model').setValue(' gpt-image-1 ')
    await wrapper.get('#call-error-query').setValue(' request-123 ')
    await wrapper.get('form').trigger('submit')
    const table = wrapper.findComponent({ name: 'OpsErrorLogTable' })
    table.vm.$emit('update:page', 4)
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 4, group_id: 3, model: 'gpt-image-1', q: 'request-123' }))
    table.vm.$emit('sort', 'status_code', 'asc')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, group_id: 3, sort_by: 'status_code', sort_order: 'asc', q: 'request-123' }))
    table.vm.$emit('update:page', 2)
    table.vm.$emit('update:pageSize', 50)
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, page_size: 50, group_id: 3, sort_by: 'status_code' }))
    table.vm.$emit('update:page', 2)
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1, group_id: undefined }))
  })

  it('resets identity, text and date filters and clears the displayed user', async () => {
    const wrapper = mountPage()
    await flushPromises()
    await chooseUser(wrapper)
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('3')
    await wrapper.get('#call-error-model').setValue('gpt-image-1')
    await wrapper.get('#call-error-query').setValue('timeout')
    const dates = wrapper.findComponent({ name: 'DateRangePicker' })
    dates.vm.$emit('update:startDate', '2026-10-01')
    dates.vm.$emit('change')
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === 'common.reset')!.trigger('click')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({
      page: 1, user_id: undefined, api_key_id: undefined, group_id: undefined, model: undefined, q: undefined,
      start_time: new Date(2026, 9, 10).toISOString(), end_time: new Date(2026, 9, 17).toISOString(),
    }))
    expect((wrapper.get('input[placeholder="admin.usage.searchUserPlaceholder"]').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('#call-error-query').element as HTMLInputElement).value).toBe('')
  })

  it('opens the selected request error in the shared details modal', async () => {
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'OpsErrorLogTable' }).vm.$emit('openErrorDetail', 42)
    await flushPromises()
    expect(wrapper.findComponent({ name: 'OpsErrorDetailModal' }).props()).toMatchObject({ show: true, errorId: 42, errorType: 'request' })
    expect(wrapper.get('[data-test="error-detail"]').text()).toBe('42')
  })

  it('keeps the newest response when earlier requests finish later', async () => {
    const first = deferred<OpsErrorLogsResponse>()
    const second = deferred<OpsErrorLogsResponse>()
    listErrorLogs.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('3')
    second.resolve(response(2))
    await flushPromises()
    first.resolve(response(1))
    await flushPromises()
    expect(wrapper.get('[data-test="error-table"]').text()).toBe('Failure 2')
  })

  it('ignores a stale failure without ending the current loading state', async () => {
    const first = deferred<OpsErrorLogsResponse>()
    const second = deferred<OpsErrorLogsResponse>()
    listErrorLogs.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const wrapper = mountPage()
    await flushPromises()
    await wrapper.get('select[aria-label="admin.usage.allGroups"]').setValue('3')
    first.reject(new Error('Old request failed'))
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'OpsErrorLogTable' }).props('loading')).toBe(true)
    second.resolve(response(2))
    await flushPromises()
    expect(wrapper.findComponent({ name: 'OpsErrorLogTable' }).props('loading')).toBe(false)
  })

  it('shows API failure details instead of an empty list and allows retry', async () => {
    listErrorLogs.mockRejectedValueOnce({ message: 'Network Error' })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('Network Error')
    expect(wrapper.get('[data-test="error-table"]').isVisible()).toBe(false)
    listErrorLogs.mockResolvedValueOnce(response(3))
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="error-table"]').text()).toBe('Failure 3')
  })

  it('explains when ops monitoring is disabled instead of reporting no errors', async () => {
    listErrorLogs.mockRejectedValueOnce({ code: 'OPS_DISABLED', message: 'Ops monitoring is disabled' })
    const wrapper = mountPage()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('usage.errors.monitoringDisabled')
    expect(wrapper.get('[data-test="error-table"]').isVisible()).toBe(false)
  })

  it.each([['2026-10-17', '2026-10-16'], ['2026-02-30', '2026-03-01'], ['', '2026-10-16']])(
    'rejects an invalid range %s to %s and prevents pending data from replacing the validation error',
    async (start, end) => {
      const pending = deferred<OpsErrorLogsResponse>()
      listErrorLogs.mockReturnValueOnce(pending.promise)
      const wrapper = mountPage()
      const dates = wrapper.findComponent({ name: 'DateRangePicker' })
      dates.vm.$emit('update:startDate', start)
      dates.vm.$emit('update:endDate', end)
      dates.vm.$emit('change')
      await flushPromises()
      expect(listErrorLogs).toHaveBeenCalledTimes(1)
      expect(wrapper.get('[role="alert"]').text()).toContain('usage.errors.invalidDateRange')
      pending.resolve(response(1))
      await flushPromises()
      expect(wrapper.findComponent({ name: 'OpsErrorLogTable' }).props()).toMatchObject({ rows: [], loading: false, total: 0 })
      expect(wrapper.get('[data-test="error-table"]').isVisible()).toBe(false)
    },
  )

  it('includes the full selected day at month boundaries and starts date searches on page one', async () => {
    const wrapper = mountPage()
    await flushPromises()
    wrapper.findComponent({ name: 'OpsErrorLogTable' }).vm.$emit('update:page', 3)
    const dates = wrapper.findComponent({ name: 'DateRangePicker' })
    dates.vm.$emit('update:startDate', '2026-09-30')
    dates.vm.$emit('update:endDate', '2026-09-30')
    dates.vm.$emit('change')
    await flushPromises()
    expect(listErrorLogs).toHaveBeenLastCalledWith(expect.objectContaining({
      page: 1, start_time: new Date(2026, 8, 30).toISOString(), end_time: new Date(2026, 9, 1).toISOString(),
    }))
  })
})
