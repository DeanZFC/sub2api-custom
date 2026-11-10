import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSmartWSModal from '../GroupSmartWSModal.vue'
import type { AdminGroup } from '@/types'
const api = vi.hoisted(() => ({ getSmartWS: vi.fn(), setSmartWS: vi.fn() }))
vi.mock('@/api/admin/groups', () => api)
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
function modal() { return mount(GroupSmartWSModal, { props: { show: true, group: { id: 5, name: 'Test' } as AdminGroup }, global: { stubs: { BaseDialog: { props: ['show'], template: '<div><slot /><slot name="footer" /></div>' } } } }) }
describe('SmartWS group policy', () => {
 beforeEach(() => { api.getSmartWS.mockReset().mockResolvedValue({ enabled: false, revision: 0 }); api.setSmartWS.mockReset().mockResolvedValue({ enabled: true, revision: 1 }) })
 it('sends the loaded revision when saving', async () => { const wrapper = modal(); await flushPromises(); await wrapper.get('input').setValue(true); await wrapper.get('.btn-primary').trigger('click'); await flushPromises(); expect(api.setSmartWS).toHaveBeenCalledWith(5, { enabled: true, revision: 0 }); expect(wrapper.emitted('close')).toHaveLength(1); wrapper.unmount() })
 it('reloads the current revision after a conflict', async () => { const wrapper = modal(); await flushPromises(); api.setSmartWS.mockRejectedValue({ response: { status: 409 } }); api.getSmartWS.mockResolvedValue({ enabled: true, revision: 8 }); await wrapper.get('.btn-primary').trigger('click'); await flushPromises(); expect(wrapper.get('[role="alert"]').text()).toContain('conflict'); api.setSmartWS.mockResolvedValue({ enabled: true, revision: 9 }); await wrapper.get('.btn-primary').trigger('click'); await flushPromises(); expect(api.setSmartWS).toHaveBeenLastCalledWith(5, { enabled: true, revision: 8 }); wrapper.unmount() })
})
