import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import UserInvoicesView from '../UserInvoicesView.vue'
import Pagination from '@/components/common/Pagination.vue'
import type { InvoiceOrder, InvoiceRequest } from '@/types/invoice'

const api = vi.hoisted(() => ({ getConfig: vi.fn(), getOrders: vi.fn(), getRequests: vi.fn(), createRequest: vi.fn(), downloadPDF: vi.fn() }))
const notices = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn() }))
const auth = vi.hoisted(() => ({ user: { balance: 100 }, refreshUser: vi.fn() }))
vi.mock('@/api/invoices', () => ({ invoiceAPI: api, saveInvoicePDF: vi.fn() }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notices }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)

const order = (id: number, pay_amount = 150): InvoiceOrder => ({ id, pay_amount, currency: 'CNY', completed_at: '2026-11-01T10:00:00Z' })
const issued: InvoiceRequest = {
  id: 12, user_id: 1, user_email: 'user@example.com', order_ids: [10], amount: 300, fee_amount: 0, fee_refunded: false, currency: 'CNY',
  buyer_type: 'personal', title: 'Existing invoice', tax_id: '', emails: ['user@example.com'], remark: '',
  status: 'issued', admin_remark: '', invoice_number: 'INV001', file_name: 'invoice.pdf',
  created_at: '2026-11-01T10:00:00Z', updated_at: '2026-11-01T10:00:00Z',
}

beforeEach(() => {
  vi.clearAllMocks()
  auth.user.balance = 100
  auth.refreshUser.mockResolvedValue(auth.user)
  api.getConfig.mockResolvedValue({ data: { enabled: true, min_amount: 300, fee_rate: 0, fee_min_amount: 0, instructions: '', currency: 'CNY' } })
  api.getOrders.mockResolvedValue({ data: { items: [order(1), order(2)], total: 2, page: 1, page_size: 20, pages: 1, available_amount: 300 } })
  api.getRequests.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20, pages: 0 } })
  api.createRequest.mockResolvedValue({ data: { ...issued, status: 'pending' } })
})

async function openPage() {
  const wrapper = mount(UserInvoicesView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
    Icon: true, InvoiceStatusBadge: true, InvoiceRequestDetails: true, Pagination: true,
  } } })
  await flushPromises()
  return wrapper
}

describe('self-service invoice requests', () => {
  it('keeps issued history accessible when requests are disabled', async () => {
    api.getConfig.mockResolvedValue({ data: { enabled: false, min_amount: 300, fee_rate: 0, fee_min_amount: 0, instructions: '', currency: 'CNY' } })
    api.getRequests.mockResolvedValue({ data: { items: [issued], total: 1 } })
    const wrapper = await openPage()
    expect(wrapper.text()).toContain('invoice.disabled')
    expect(wrapper.text()).toContain('Existing invoice')
    expect(wrapper.text()).toContain('invoice.download')
    expect(wrapper.find('[data-testid="invoice-open-form"]').exists()).toBe(false)
    expect(api.getOrders).not.toHaveBeenCalled()
  })

  it('requires the selected actual payments to reach the configured minimum', async () => {
    const wrapper = await openPage()
    const applyButton = wrapper.get('[data-testid="invoice-open-form"]')
    expect(applyButton.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="invoice-order-1"]').setValue(true)
    expect(applyButton.attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="invoice-order-2"]').setValue(true)
    expect(applyButton.attributes('disabled')).toBeUndefined()
  })

  it('retains order selection across pages and merges its actual payments', async () => {
    api.getOrders.mockImplementation(({ page }: { page: number }) => Promise.resolve({ data: {
      items: [order(page)], total: 21, page, page_size: 20, pages: 2, available_amount: 300,
    } }))
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-order-1"]').setValue(true)
    wrapper.getComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    await wrapper.get('[data-testid="invoice-order-2"]').setValue(true)
    expect(wrapper.get('[data-testid="invoice-open-form"]').attributes('disabled')).toBeUndefined()
    wrapper.getComponent(Pagination).vm.$emit('update:page', 1)
    await flushPromises()
    expect((wrapper.get('[data-testid="invoice-order-1"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example person')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('first@example.com, second@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    await flushPromises()
    expect(api.createRequest).toHaveBeenCalledWith({ order_ids: [1, 2], buyer_type: 'personal', title: 'Example person', tax_id: '', emails: ['first@example.com', 'second@example.com'], remark: '' })
    expect(auth.refreshUser).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-testid="invoice-open-form"]').attributes('disabled')).toBeDefined()
  })

  it('prevents repeated submission while the first request is in flight', async () => {
    let finish!: (value: unknown) => void
    api.createRequest.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-select-page"]').setValue(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example person')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    await wrapper.get('#invoice-request-form').trigger('submit')
    expect(api.createRequest).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="invoice-submit"]').attributes('disabled')).toBeDefined()
    finish({ data: { ...issued, status: 'pending' } })
    await flushPromises()
    expect(notices.showSuccess).toHaveBeenCalledWith('invoice.submitSuccess')
  })

  it('requires a tax ID for a business invoice and rejects invalid recipient emails', async () => {
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-select-page"]').setValue(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    await wrapper.get('input[value="company"]').setValue(true)
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example company')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    expect(notices.showError).toHaveBeenLastCalledWith('invoice.errors.requiredTaxId')
    await wrapper.get('[data-testid="invoice-tax-id"]').setValue('91310000123456789X')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('invalid-email')
    await wrapper.get('#invoice-request-form').trigger('submit')
    expect(notices.showError).toHaveBeenLastCalledWith('invoice.errors.invalidEmails')
    expect(api.createRequest).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('[data-testid="invoice-tax-id"]').setValue('bad-tax-id')
    await wrapper.get('#invoice-request-form').trigger('submit')
    expect(notices.showError).toHaveBeenLastCalledWith('invoice.errors.invalidTaxId')
    await wrapper.get('[data-testid="invoice-tax-id"]').setValue('91310000123456789x')
    await wrapper.get('#invoice-request-form').trigger('submit')
    await flushPromises()
    expect(api.createRequest).toHaveBeenCalledWith(expect.objectContaining({ tax_id: '91310000123456789X' }))
  })

  it.each([
    { amount: 100, rate: 10, minimum: 0, expected: '10.00' },
    { amount: 100, rate: 10, minimum: 20, expected: '20.00' },
    { amount: 300, rate: 10, minimum: 20, expected: '30.00' },
    { amount: 100.05, rate: 10, minimum: 0, expected: '10.01' },
    { amount: 100, rate: 0, minimum: 20, expected: '20.00' },
  ])('previews the fee for $amount at $rate% with a minimum of $minimum', async ({ amount, rate, minimum, expected }) => {
    api.getConfig.mockResolvedValue({ data: { enabled: true, min_amount: 100, fee_rate: rate, fee_min_amount: minimum, instructions: '', currency: 'CNY' } })
    api.getOrders.mockResolvedValue({ data: { items: [order(1, amount)], total: 1, available_amount: amount } })
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-order-1"]').setValue(true)
    expect(wrapper.get('[data-testid="invoice-fee-preview"]').text()).toContain(expected)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    expect(wrapper.get('[data-testid="invoice-submit-fee"]').text()).toBe(expected)
  })

  it('prevents submission when the balance cannot cover the fee', async () => {
    api.getConfig.mockResolvedValue({ data: { enabled: true, min_amount: 100, fee_rate: 10, fee_min_amount: 20, instructions: '', currency: 'CNY' } })
    auth.user.balance = 19.99
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-order-1"]').setValue(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    expect(wrapper.get('[data-testid="invoice-submit"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toBe('invoice.errors.INVOICE_INSUFFICIENT_BALANCE')
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example person')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    expect(api.createRequest).not.toHaveBeenCalled()
  })

  it('allows a free invoice request even when the account balance is negative', async () => {
    auth.user.balance = -1
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-select-page"]').setValue(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    expect(wrapper.get('[data-testid="invoice-submit-fee"]').text()).toBe('0.00')
    expect(wrapper.get('[data-testid="invoice-submit"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example person')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    await flushPromises()
    expect(api.createRequest).toHaveBeenCalledTimes(1)
    expect(notices.showSuccess).toHaveBeenCalledWith('invoice.submitSuccess')
  })

  it('preserves the application when a changed server balance rejects the fee', async () => {
    api.getConfig.mockResolvedValue({ data: { enabled: true, min_amount: 100, fee_rate: 10, fee_min_amount: 20, instructions: '', currency: 'CNY' } })
    api.createRequest.mockRejectedValue({ reason: 'INVOICE_INSUFFICIENT_BALANCE' })
    const wrapper = await openPage()
    await wrapper.get('[data-testid="invoice-order-1"]').setValue(true)
    await wrapper.get('[data-testid="invoice-open-form"]').trigger('click')
    await wrapper.get('[data-testid="invoice-title"]').setValue('Example person')
    await wrapper.get('[data-testid="invoice-emails"]').setValue('user@example.com')
    await wrapper.get('#invoice-request-form').trigger('submit')
    await flushPromises()
    expect(notices.showSuccess).not.toHaveBeenCalled()
    expect((wrapper.get('[data-testid="invoice-title"]').element as HTMLInputElement).value).toBe('Example person')
    expect((wrapper.get('[data-testid="invoice-order-1"]').element as HTMLInputElement).checked).toBe(true)
    expect(auth.refreshUser).toHaveBeenCalledTimes(1)
  })
})
