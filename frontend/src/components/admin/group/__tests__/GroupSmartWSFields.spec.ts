import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { SmartWSPolicy } from '@/api/admin/groups'
import GroupSmartWSFields from '../GroupSmartWSFields.vue'

const api = vi.hoisted(() => ({ getSmartWS: vi.fn(), setSmartWS: vi.fn() }))
vi.mock('@/api/admin/groups', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const mountFields = () => mount(GroupSmartWSFields, { props: { groupId: 5 } })

describe('SmartWS group fields', () => {
  beforeEach(() => {
    api.getSmartWS.mockReset().mockResolvedValue({ enabled: false, revision: 0 })
    api.setSmartWS.mockReset().mockResolvedValue({ enabled: true, revision: 1 })
  })

  it('sends the loaded revision only when the setting changes', async () => {
    const wrapper = mountFields()
    await flushPromises()
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).not.toHaveBeenCalled()
    await wrapper.get('input').setValue(true)
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).toHaveBeenCalledWith(5, { enabled: true, revision: 0 })
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('reloads after a conflict and uses the latest revision for the next change', async () => {
    const wrapper = mountFields()
    await flushPromises()
    await wrapper.get('input').setValue(true)
    api.setSmartWS.mockRejectedValueOnce({ response: { status: 409 } })
    api.getSmartWS.mockResolvedValue({ enabled: true, revision: 8 })
    expect(await wrapper.vm.save()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('conflict')
    expect(wrapper.get('input').element.checked).toBe(true)
    await wrapper.get('input').setValue(false)
    api.setSmartWS.mockResolvedValue({ enabled: false, revision: 9 })
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).toHaveBeenLastCalledWith(5, { enabled: false, revision: 8 })
    wrapper.unmount()
  })

  it('does not replay a write after a timeout when reloading confirms its value', async () => {
    const wrapper = mountFields()
    await flushPromises()
    await wrapper.get('input').setValue(true)
    api.setSmartWS.mockRejectedValueOnce(new Error('timeout'))
    api.getSmartWS.mockResolvedValue({ enabled: true, revision: 1 })
    expect(await wrapper.vm.save()).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('saveFailed')
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('blocks retries when an uncertain write cannot be reloaded', async () => {
    const wrapper = mountFields()
    await flushPromises()
    await wrapper.get('input').setValue(true)
    api.setSmartWS.mockRejectedValueOnce(new Error('timeout'))
    api.getSmartWS.mockRejectedValueOnce(new Error('offline'))
    expect(await wrapper.vm.save()).toBe(false)
    expect(wrapper.vm.validate()).toBe(false)
    expect(wrapper.get('input').element.disabled).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('loadFailed')
    expect(await wrapper.vm.save()).toBe(false)
    expect(api.setSmartWS).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })

  it('leaves unrelated fields saveable when the initial policy load fails', async () => {
    api.getSmartWS.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountFields()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('loadFailed')
    expect(wrapper.get('input').element.disabled).toBe(true)
    expect(wrapper.vm.validate()).toBe(true)
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('waits for loading and ignores stale reads after switching groups', async () => {
    let resolveFirst!: (policy: SmartWSPolicy) => void
    api.getSmartWS.mockImplementationOnce(() => new Promise<SmartWSPolicy>(resolve => { resolveFirst = resolve }))
    const wrapper = mountFields()
    expect(wrapper.vm.validate()).toBe(false)
    expect(await wrapper.vm.save()).toBe(false)
    api.getSmartWS.mockResolvedValue({ enabled: true, revision: 7 })
    await wrapper.setProps({ groupId: 6 })
    await flushPromises()
    resolveFirst({ enabled: false, revision: 0 })
    await flushPromises()
    expect(wrapper.get('input').element.checked).toBe(true)
    await wrapper.get('input').setValue(false)
    api.setSmartWS.mockResolvedValue({ enabled: false, revision: 8 })
    expect(await wrapper.vm.save()).toBe(true)
    expect(api.setSmartWS).toHaveBeenCalledWith(6, { enabled: false, revision: 7 })
    wrapper.unmount()
  })

  it('discards unsaved changes when the editor unmounts', async () => {
    const wrapper = mountFields()
    await flushPromises()
    await wrapper.get('input').setValue(true)
    wrapper.unmount()
    expect(api.setSmartWS).not.toHaveBeenCalled()
    const reopened = mountFields()
    await flushPromises()
    expect(reopened.get('input').element.checked).toBe(false)
    reopened.unmount()
  })
})
