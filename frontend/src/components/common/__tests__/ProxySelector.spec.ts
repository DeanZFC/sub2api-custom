import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { Proxy } from '@/types'
import ProxySelector from '../ProxySelector.vue'

vi.mock('@/api/admin', () => ({ default: { proxies: {} } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const proxies = [
  { id: 11, name: 'Proxy A', protocol: 'http', host: 'a.example', port: 8080 },
  { id: 22, name: 'Proxy B', protocol: 'http', host: 'b.example', port: 8080 }
] as Proxy[]

function mountSelector(modelValue: number | null | number[], multiple = false) {
  return mount(ProxySelector, { props: { modelValue, multiple, proxies }, global: { stubs: { Icon: true } } })
}

describe('ProxySelector', () => {
  it('keeps scalar selection and closes for single-select consumers', async () => {
    const wrapper = mountSelector(null)
    await wrapper.get('.select-trigger').trigger('click')
    await wrapper.findAll('.select-option')[1]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([[11]])
    expect(wrapper.get('.select-trigger').classes()).not.toContain('select-trigger-open')
    wrapper.unmount()
  })

  it('adds and removes proxies without mutating the input or closing the menu', async () => {
    const original = [11]
    const wrapper = mountSelector(original, true)
    expect(wrapper.get('.select-value').text()).toContain('Proxy A')
    await wrapper.get('.select-trigger').trigger('click')
    await wrapper.findAll('.select-option')[2]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[11, 22]])
    expect(original).toEqual([11])
    expect(wrapper.find('.select-dropdown').exists()).toBe(true)
    await wrapper.setProps({ modelValue: [11, 22] })
    await wrapper.findAll('.select-option')[1]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[22]])
    await wrapper.findAll('.select-option')[0]!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[]])
    wrapper.unmount()
  })
})

describe('managed IPv6 selection', () => {
  const managed = [
    ...proxies,
    { id: 33, name: 'reserved', protocol: 'http', host: 'private', port: 8080, managed_ipv6: '2001:db8::33', managed_ipv6_state: 'reserved' },
    { id: 44, name: 'other-owner', protocol: 'http', host: 'private', port: 8080, managed_ipv6: '2001:db8::44', managed_ipv6_state: 'bound' }
  ] as Proxy[]

  it('keeps an IPv6 exclusive when moving to and from a normal multi-proxy pool', async () => {
    const wrapper = mount(ProxySelector, { props: { modelValue: [11, 22], multiple: true, proxies: managed }, global: { stubs: { Icon: true } } })
    await wrapper.get('.select-trigger').trigger('click')
    expect(wrapper.text()).not.toContain('other-owner')
    await wrapper.findAll('.select-option').find(option => option.text().includes('reserved'))!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[33]])
    await wrapper.setProps({ modelValue: [33] })
    expect(wrapper.get('.select-value').text()).toContain('2001:db8::33')
    await wrapper.findAll('.select-option').find(option => option.text().includes('Proxy A'))!.trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)).toEqual([[11]])
    wrapper.unmount()
  })
})
