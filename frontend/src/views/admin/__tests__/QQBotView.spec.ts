import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import QQBotView from '../QQBotView.vue'

const api = vi.hoisted(() => ({ config: vi.fn(), save: vi.fn(), runtime: vi.fn(), probe: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/admin/QQBotBindings.vue', () => ({ default: { template: '<section />' } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('QQ bot settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.config.mockResolvedValue({ enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, allowed_groups: [], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_turns: 6 })
    api.runtime.mockResolvedValue({ groups: [], jobs: [], rewards: [], bindings: 0, encryption_ready: true })
    api.save.mockImplementation(async (c) => c)
  })
  it('keeps stored secrets blank and disables probes until changes are saved', async () => {
    const wrapper = mount(QQBotView, { global: { stubs: { AppLayout: { template: '<main><slot /></main>' } } } })
    await flushPromises()
    expect((wrapper.find('input[type="password"]').element as HTMLInputElement).value).toBe('')
    const chatButton = () => wrapper.findAll('button').find(b => b.text() === 'qqbot.test_chat')!
    expect(chatButton().attributes('disabled')).toBeUndefined()
    await wrapper.find('input[placeholder="https://api.example.com/v1"]').setValue('https://other.invalid/v1')
    expect(chatButton().attributes('disabled')).toBeDefined()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ api_key: '', app_secret: '', base_url: 'https://other.invalid/v1' }))
  })
})
