import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import zh from '@/i18n/locales/zh'
import appRouter from '@/router'

vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/views/user/UserOrdersView.vue', () => ({ default: { template: '<p>Orders</p>' } }))
vi.mock('@/views/user/UserInvoicesView.vue', () => ({ default: { template: '<p>Invoices</p>' } }))
vi.mock('@/views/user/PaymentView.vue', () => ({ default: { template: '<p>Recharge</p>' } }))
enableAutoUnmount(afterEach)

describe('My Orders navigation', () => {
  const createTestRouter = () => createRouter({ history: createMemoryHistory(), routes: appRouter.options.routes })

  it('opens the order list first and shows the invoice panel from the second tab', async () => {
    const router = createTestRouter()
    await router.push('/orders')
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh } })
    const page = mount(RouterView, { global: { plugins: [router, i18n] } })
    const links = page.findAll('nav a')
    expect(links.map(link => link.text())).toEqual(['订单列表', '自助开票'])
    expect(links[0].attributes('aria-current')).toBe('page')
    expect(page.get('p').text()).toBe('Orders')

    await links[1].trigger('click')
    await flushPromises()
    expect(links[1].attributes('aria-current')).toBe('page')
    expect(page.get('p').text()).toBe('Invoices')
    expect(router.currentRoute.value.meta).toMatchObject({ requiresAuth: true, requiresPayment: true })
  })

  it('redirects old bookmarks to invoicing and recharge', async () => {
    const router = createTestRouter()
    await router.push('/invoices')
    expect(router.currentRoute.value.path).toBe('/orders/invoices')
    await router.push('/coupons')
    expect(router.currentRoute.value.path).toBe('/purchase')
  })
})
