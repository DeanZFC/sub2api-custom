import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import ExcelAuthorizationPanel from '../ExcelAuthorizationPanel.vue'
import * as api from '@/api/admin/excelAuthorization'
vi.mock('@/api/admin/excelAuthorization', () => ({ getExcelAuthorization: vi.fn(), getCredentialEncryption: vi.fn(), getReauthRuntime: vi.fn(), initializeCredentialEncryption: vi.fn(), saveExcelAuthorization: vi.fn(), startExcelAuthorization: vi.fn() }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
const account = { id: 1, credentials: { email: 'user@example.com' } } as any
const config = { account_id: 1, login_email: 'user@example.com', credential_mode: 'password_totp', engine: 'local_worker', proxy_source: 'account', password_configured: true, totp_configured: true, configured: true } as const
async function open() {
  const wrapper = mount(ExcelAuthorizationPanel, { props: { account } })
  await wrapper.find('button').trigger('click')
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getExcelAuthorization).mockResolvedValue({ config })
  vi.mocked(api.getCredentialEncryption).mockResolvedValue({ configured: true, source: 'local_file' })
  vi.mocked(api.getReauthRuntime).mockResolvedValue({ mode: 'managed', state: 'running' })
  vi.mocked(api.saveExcelAuthorization).mockResolvedValue(config)
})
afterEach(() => vi.useRealTimers())
describe('BPS credential material handling', () => {
  it('keeps stored secrets when blank and clears new secret input after saving', async () => {
    const wrapper = await open()
    await wrapper.find('[data-testid="excel-save"]').trigger('click')
    await flushPromises()
    expect(api.saveExcelAuthorization).toHaveBeenCalledWith(1, expect.objectContaining({ password: undefined, totp_secret: undefined, clear_password: false, clear_totp: false }))
    await wrapper.find('[data-testid="excel-password"]').setValue('new-private-password')
    await wrapper.find('[data-testid="excel-totp"]').setValue('private-totp')
    await wrapper.find('[data-testid="excel-save"]').trigger('click')
    await flushPromises()
    expect((wrapper.find('[data-testid="excel-password"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.html()).not.toContain('new-private-password')
    wrapper.unmount()
  })
  it('drops secrets and ignores old responses when switching accounts', async () => {
    const wrapper = await open()
    await wrapper.find('[data-testid="excel-password"]').setValue('private-password')
    await wrapper.setProps({ account: { ...account, id: 2 } })
    expect(wrapper.find('[data-testid="excel-password"]').exists()).toBe(false)
    expect((wrapper.vm as any).password).toBe('')
    let resolve!: (value: api.ExcelAuthorizationStatus) => void
    vi.mocked(api.getExcelAuthorization).mockReturnValueOnce(new Promise(r => { resolve = r }))
    await wrapper.find('button').trigger('click')
    await wrapper.setProps({ active: false })
    resolve({ config })
    await flushPromises()
    expect((wrapper.vm as any).status).toEqual({})
    wrapper.unmount()
  })
  it('polls active tasks and stops polling when the panel closes', async () => {
    vi.useFakeTimers()
    vi.mocked(api.getExcelAuthorization).mockResolvedValue({ config, task: { id: 3, status: 'queued', stage: 'pending', oauth_profile: 'excel' } })
    const wrapper = await open()
    await vi.advanceTimersByTimeAsync(3000)
    expect(api.getExcelAuthorization).toHaveBeenCalledTimes(2)
    await wrapper.find('button').trigger('click')
    await vi.advanceTimersByTimeAsync(6000)
    expect(api.getExcelAuthorization).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
