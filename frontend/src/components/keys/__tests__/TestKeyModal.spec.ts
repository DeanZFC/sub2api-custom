import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import TestKeyModal from '../TestKeyModal.vue'
import Select from '@/components/common/Select.vue'
import type { ApiKey } from '@/types'

const { models, run } = vi.hoisted(() => ({ models: vi.fn(), run: vi.fn() }))
vi.mock('@/api/keyTest', async () => ({
  ...await vi.importActual<typeof import('@/api/keyTest')>('@/api/keyTest'),
  getKeyTestModels: models,
  runKeyTest: run,
}))
vi.mock('@/api/client', () => ({ apiClient: {} }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const key = { id: 7, name: 'My key', key: 'sk-masked...1234', status: 'active', group: { name: 'Text group' } } as ApiKey
const mountModal = () => mount(TestKeyModal, {
  props: { show: true, apiKey: key },
  global: { stubs: { BaseDialog: {
    name: 'BaseDialog',
    props: ['show', 'closeOnEscape', 'showCloseButton'],
    emits: ['close'],
    template: '<div v-if="show"><slot/><slot name="footer"/></div>',
  }, teleport: true } },
})
const select = (wrapper: ReturnType<typeof mountModal>, id: string) => wrapper.findAllComponents(Select).find(component => component.props('id') === id)!

beforeEach(() => {
  vi.clearAllMocks()
  models.mockResolvedValue(['text-model-a', 'text-model-b'])
  run.mockResolvedValue({ model: 'text-model-b', choices: [{ message: { content: 'OK' } }], usage: { prompt_tokens: 10, completion_tokens: 2 } })
})

describe('TestKeyModal', () => {
  it('shows the key card, model and mode dropdowns, and ready console without spending quota', async () => {
    const wrapper = mountModal()
    await flushPromises()
    expect(models).toHaveBeenCalledWith(7, expect.any(AbortSignal))
    expect(run).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="test-key-info"]').text()).toContain('My key')
    expect(wrapper.get('[data-test="test-key-info"]').text()).toContain('Text group')
    expect(wrapper.get('[data-test="test-key-info"]').text()).toContain('active')
    expect(select(wrapper, 'key-test-model').props('options')).toHaveLength(2)
    expect(select(wrapper, 'key-test-mode').props('modelValue')).toBe('regular')
    expect(select(wrapper, 'key-test-mode').props('options')).toEqual([{ value: 'regular', label: 'keys.test.modeRegular' }])
    expect(wrapper.get('[data-test="test-console"]').text()).toContain('keys.test.ready')
    expect(wrapper.get('[data-test="test-billing-notice"]').text()).toContain('keys.test.billingHint')
    expect(wrapper.find('#key-test-prompt').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(key.key)
    wrapper.unmount()
  })

  it('sends the selected key and regular hi prompt only after submit', async () => {
    const wrapper = mountModal()
    await flushPromises()
    select(wrapper, 'key-test-model').vm.$emit('update:modelValue', 'text-model-b')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(run).toHaveBeenCalledWith(7, 'text-model-b', expect.any(AbortSignal))
    expect(wrapper.get('[data-test="test-result"]').text()).toContain('OK')
    expect(wrapper.get('[data-test="test-result"]').text()).toContain('10')
    expect(wrapper.get('[data-test="run-key-test"]').text()).toBe('keys.test.retry')
    expect(wrapper.emitted('tested')).toHaveLength(1)
    wrapper.unmount()
  })

  it('prevents duplicate requests and closing while a charged test is running', async () => {
    let resolve!: (value: unknown) => void
    run.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await wrapper.get('form').trigger('submit')
    expect(run).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="run-key-test"]').attributes('disabled')).toBeDefined()
    expect(select(wrapper, 'key-test-model').props('disabled')).toBe(true)
    expect(select(wrapper, 'key-test-mode').props('disabled')).toBe(true)
    const dialog = wrapper.findComponent({ name: 'BaseDialog' })
    expect(dialog.props('closeOnEscape')).toBe(false)
    dialog.vm.$emit('close')
    expect(wrapper.emitted('close')).toBeUndefined()
    resolve({ model: 'text-model-a', choices: [{ message: { content: 'OK' } }] })
    await flushPromises()
    expect(dialog.props('closeOnEscape')).toBe(true)
    wrapper.unmount()
  })

  it('shows quota or gateway failures and never automatically retries', async () => {
    run.mockRejectedValue({ error: { message: 'API key quota exhausted' } })
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-test="test-error"]').text()).toContain('API key quota exhausted')
    expect(wrapper.find('[data-test="test-result"]').exists()).toBe(false)
    expect(run).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="run-key-test"]').text()).toBe('keys.test.retry')
    expect(wrapper.emitted('tested')).toHaveLength(1)
    wrapper.unmount()
  })

  it('allows manual model entry when discovery fails and discards stale model requests', async () => {
    let resolve!: (value: string[]) => void
    models.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    models.mockRejectedValueOnce({ message: 'Unavailable' })
    const wrapper = mountModal()
    const oldSignal = models.mock.calls[0][1] as AbortSignal
    await wrapper.setProps({ apiKey: { ...key, id: 8 } })
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    resolve(['stale-model'])
    await flushPromises()
    expect(select(wrapper, 'key-test-model').props('options')).toHaveLength(0)
    expect(select(wrapper, 'key-test-model').props('creatable')).toBe(true)
    expect(wrapper.text()).toContain('Unavailable')
    select(wrapper, 'key-test-model').vm.$emit('update:modelValue', 'manual-model')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(run).toHaveBeenCalledWith(8, 'manual-model', expect.any(AbortSignal))
    wrapper.unmount()
  })

  it('ignores a stale test response after the selected key changes', async () => {
    let resolve!: (value: unknown) => void
    run.mockImplementation(() => new Promise(done => { resolve = done }))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    const signal = run.mock.calls[0][2] as AbortSignal
    await wrapper.setProps({ apiKey: { ...key, id: 8 } })
    resolve({ model: 'old-key-model', choices: [{ message: { content: 'Stale output' } }] })
    await flushPromises()
    expect(signal.aborted).toBe(true)
    expect(wrapper.find('[data-test="test-result"]').exists()).toBe(false)
    expect(wrapper.emitted('tested')).toBeUndefined()
    wrapper.unmount()
  })
})
