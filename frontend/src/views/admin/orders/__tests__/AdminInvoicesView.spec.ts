import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import AdminInvoicesView from '../AdminInvoicesView.vue'
import type { InvoiceConfig } from '@/types/invoice'

const api = vi.hoisted(() => ({ getConfig: vi.fn(), updateConfig: vi.fn(), getRequests: vi.fn() }))
const notices = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/invoices', () => ({ adminInvoiceAPI: api, saveInvoicePDF: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notices }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.clearAllMocks()
  api.getConfig.mockResolvedValue({ data: { enabled: true, min_amount: 300, fee_rate: 10, fee_min_amount: 20, instructions: '', currency: 'CNY' } })
  api.getRequests.mockResolvedValue({ data: { items: [], total: 0 } })
  api.updateConfig.mockImplementation((data: Partial<InvoiceConfig>) => Promise.resolve({ data: { ...data, currency: 'CNY' } }))
})

async function openPage() {
  const wrapper = mount(AdminInvoicesView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: true, Icon: true, InvoiceStatusBadge: true, InvoiceRequestDetails: true,
    Pagination: true, Select: true, Toggle: true,
  } } })
  await flushPromises()
  return wrapper
}

describe('invoice handling fee settings', () => {
  it('loads and saves the percentage and minimum handling fee together', async () => {
    const wrapper = await openPage()
    expect((wrapper.get('[data-testid="invoice-fee-rate"]').element as HTMLInputElement).value).toBe('10')
    expect((wrapper.get('[data-testid="invoice-fee-min-amount"]').element as HTMLInputElement).value).toBe('20')
    await wrapper.get('[data-testid="invoice-fee-rate"]').setValue(12.5)
    await wrapper.get('[data-testid="invoice-fee-min-amount"]').setValue(25.25)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.updateConfig).toHaveBeenCalledWith({ enabled: true, min_amount: 300, fee_rate: 12.5, fee_min_amount: 25.25, instructions: '' })
    expect(notices.showSuccess).toHaveBeenCalledWith('invoice.saveSuccess')
  })

  it.each([
    { field: 'rate', value: -1, error: 'INVALID_INVOICE_FEE_RATE' },
    { field: 'rate', value: 100.01, error: 'INVALID_INVOICE_FEE_RATE' },
    { field: 'rate', value: 10.001, error: 'INVALID_INVOICE_FEE_RATE' },
    { field: 'rate', value: '', error: 'INVALID_INVOICE_FEE_RATE' },
    { field: 'min-amount', value: -1, error: 'INVALID_INVOICE_FEE_MIN_AMOUNT' },
    { field: 'min-amount', value: 1_000_000_000.01, error: 'INVALID_INVOICE_FEE_MIN_AMOUNT' },
    { field: 'min-amount', value: 20.001, error: 'INVALID_INVOICE_FEE_MIN_AMOUNT' },
    { field: 'min-amount', value: '', error: 'INVALID_INVOICE_FEE_MIN_AMOUNT' },
  ])('rejects invalid $field = $value before saving', async ({ field, value, error }) => {
    const wrapper = await openPage()
    await wrapper.get(`[data-testid="invoice-fee-${field}"]`).setValue(value)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.updateConfig).not.toHaveBeenCalled()
    expect(notices.showError).toHaveBeenCalledWith(`invoice.errors.${error}`)
  })

  it('allows zero values to disable handling fees', async () => {
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-fee-rate"]').setValue(0)
    await wrapper.get('[data-testid="invoice-fee-min-amount"]').setValue(0)
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.updateConfig).toHaveBeenCalledWith(expect.objectContaining({ fee_rate: 0, fee_min_amount: 0 }))
  })
})
