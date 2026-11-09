<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="card p-5 sm:p-6">
        <div class="flex flex-wrap items-start justify-between gap-5">
          <div class="min-w-0 flex-1 space-y-3">
            <h2 class="flex items-center gap-2 text-lg font-semibold"><Icon name="document" class="text-primary-500" />{{ t('invoice.instructionsTitle') }}</h2>
            <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('invoice.description', { amount: config.min_amount.toFixed(2) }) }}</p>
            <ul class="list-inside list-disc space-y-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
              <li>{{ t('invoice.eligibleHint') }}</li>
              <li>{{ t('invoice.selectionHint') }}</li>
              <li>{{ t('invoice.deliveryHint') }}</li>
            </ul>
            <p v-if="config.instructions" class="whitespace-pre-wrap break-words text-sm text-gray-600 dark:text-gray-300">{{ config.instructions }}</p>
          </div>
          <div class="flex items-center gap-3">
            <div v-if="config.enabled" class="rounded-xl bg-primary-50 px-5 py-4 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">
              <p class="text-xs">{{ t('invoice.availableAmount') }}</p>
              <p class="mt-1 text-2xl font-semibold">¥{{ availableAmount.toFixed(2) }}</p>
              <p class="mt-1 text-xs">{{ t('invoice.threshold', { amount: config.min_amount.toFixed(2) }) }}</p>
            </div>
            <button type="button" class="btn btn-secondary" :disabled="loading || submitting" @click="refresh">
              <Icon name="refresh" :class="loading ? 'animate-spin' : ''" />{{ t('common.refresh') }}
            </button>
          </div>
        </div>
        <p v-if="!loading && !config.enabled" role="status" class="mt-4 rounded-xl bg-amber-50 p-4 text-sm text-amber-800 dark:bg-amber-900/20 dark:text-amber-300">{{ t('invoice.disabled') }}</p>
      </section>

      <section v-if="config.enabled" class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-100 p-5 dark:border-dark-700 sm:p-6">
          <div><h2 class="font-semibold">{{ t('invoice.eligibleOrders') }}</h2><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.selectionHint') }}</p></div>
          <div class="flex flex-wrap items-center gap-4">
            <div class="text-sm"><p class="text-gray-500 dark:text-gray-400">{{ t('invoice.selected', { count: selectedOrders.size }) }}</p><p class="mt-1 text-xl font-semibold text-primary-600 dark:text-primary-400">¥{{ selectedAmount.toFixed(2) }} <span class="text-sm font-normal text-gray-400">/ ¥{{ config.min_amount.toFixed(2) }}</span></p></div>
            <button type="button" data-testid="invoice-open-form" class="btn btn-primary" :disabled="!canApply" @click="openForm"><Icon name="document" />{{ t('invoice.openForm') }}</button>
          </div>
        </div>
        <div class="overflow-x-auto">
          <table class="w-full min-w-[720px] text-left text-sm">
            <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr>
              <th class="w-12 px-5 py-3"><input data-testid="invoice-select-page" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :aria-label="t('invoice.selectPage')" :checked="allPageSelected" :disabled="ordersLoading || submitting || !orders.length" @change="togglePage" /></th>
              <th class="px-4 py-3 font-medium">{{ t('invoice.orderId') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.paidAmount') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.completedAt') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.status') }}</th>
            </tr></thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-if="ordersLoading"><td colspan="5" class="p-8 text-center text-gray-400">{{ t('common.loading') }}</td></tr>
              <tr v-else-if="!orders.length"><td colspan="5" class="p-10 text-center text-gray-400">{{ t('invoice.noOrders') }}</td></tr>
              <tr v-for="order in ordersLoading ? [] : orders" :key="order.id" class="hover:bg-gray-50/70 dark:hover:bg-dark-800/60">
                <td class="px-5 py-4"><input :data-testid="`invoice-order-${order.id}`" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" :aria-label="`${t('invoice.orderId')} #${order.id}`" :checked="selectedOrders.has(order.id)" :disabled="submitting" @change="toggleOrder(order)" /></td>
                <td class="px-4 py-4 font-mono">#{{ order.id }}</td><td class="px-4 py-4 font-semibold">¥{{ order.pay_amount.toFixed(2) }}</td><td class="whitespace-nowrap px-4 py-4 text-gray-500 dark:text-gray-400">{{ formatOrderDateTime(order.completed_at || '') }}</td><td class="px-4 py-4"><span class="whitespace-nowrap rounded-full bg-primary-50 px-2.5 py-1 text-xs text-primary-700 dark:bg-primary-900/20 dark:text-primary-300">{{ t('invoice.eligible') }}</span></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 p-4 dark:border-dark-700">
          <button type="button" class="text-sm text-gray-500 hover:text-primary-600 disabled:opacity-50" :disabled="!selectedOrders.size || submitting" @click="selectedOrders = new Map()">{{ t('invoice.clearSelection') }}</button>
          <Pagination v-if="orderPagination.total > 0" :page="orderPagination.page" :total="orderPagination.total" :page-size="orderPagination.page_size" @update:page="changeOrderPage" @update:page-size="changeOrderPageSize" />
        </div>
      </section>

      <section class="card overflow-hidden">
        <h2 class="border-b border-gray-100 p-5 font-semibold dark:border-dark-700">{{ t('invoice.history') }}</h2>
        <div class="overflow-x-auto"><table class="w-full min-w-[840px] text-left text-sm">
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr><th class="px-5 py-3 font-medium">{{ t('invoice.requestId') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.invoiceTitle') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.amount') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.status') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.createdAt') }}</th><th class="px-4 py-3 font-medium">{{ t('common.actions') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-if="historyLoading"><td colspan="6" class="p-8 text-center text-gray-400">{{ t('common.loading') }}</td></tr>
            <tr v-else-if="!requests.length"><td colspan="6" class="p-10 text-center text-gray-400">{{ t('invoice.noRequests') }}</td></tr>
            <tr v-for="request in historyLoading ? [] : requests" :key="request.id"><td class="px-5 py-4 font-mono">#{{ request.id }}</td><td class="max-w-64 break-words px-4 py-4">{{ request.title }}</td><td class="px-4 py-4 font-semibold">¥{{ request.amount.toFixed(2) }}</td><td class="px-4 py-4"><InvoiceStatusBadge :status="request.status" /></td><td class="whitespace-nowrap px-4 py-4 text-gray-500 dark:text-gray-400">{{ formatOrderDateTime(request.created_at) }}</td><td class="px-4 py-4"><div class="flex items-center gap-3 whitespace-nowrap"><button class="text-primary-600 hover:underline dark:text-primary-400" @click="selectedRequest = request">{{ t('common.view') }}</button><button v-if="request.status === 'issued'" class="inline-flex items-center gap-1 text-primary-600 disabled:opacity-50 dark:text-primary-400" :disabled="downloadingIds.has(request.id)" @click="download(request)"><Icon name="download" size="sm" />{{ t('invoice.download') }}</button></div></td></tr>
          </tbody>
        </table></div>
        <Pagination v-if="historyPagination.total > 0" :page="historyPagination.page" :total="historyPagination.total" :page-size="historyPagination.page_size" class="p-4" @update:page="changeHistoryPage" @update:page-size="changeHistoryPageSize" />
      </section>
    </div>

    <BaseDialog :show="showForm" :title="t('invoice.openForm')" width="wide" :close-on-escape="!submitting" :show-close-button="!submitting" @close="closeForm">
      <form id="invoice-request-form" class="space-y-5" @submit.prevent="submitRequest">
        <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl bg-primary-50 p-4 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300"><div><p class="font-medium">{{ t('invoice.selectedSummary', { count: selectedOrders.size }) }}</p><p class="mt-1 text-sm">{{ t('invoice.submitHint') }}</p></div><p class="text-2xl font-semibold">¥{{ selectedAmount.toFixed(2) }}</p></div>
        <fieldset :disabled="submitting" class="space-y-5">
          <div><p class="input-label">{{ t('invoice.buyerType') }}</p><div class="mt-2 grid gap-3 sm:grid-cols-2">
            <label class="flex cursor-pointer items-start gap-3 rounded-xl border p-4" :class="form.buyer_type === 'personal' ? 'border-primary-500 bg-primary-50/50 dark:bg-primary-900/10' : 'border-gray-200 dark:border-dark-600'"><input v-model="form.buyer_type" type="radio" value="personal" name="buyer_type" class="mt-1 text-primary-600 focus:ring-primary-500" /><span><span class="font-medium">{{ t('invoice.personal') }}</span><span class="mt-1 block text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.personalHint') }}</span></span></label>
            <label class="flex cursor-pointer items-start gap-3 rounded-xl border p-4" :class="form.buyer_type === 'company' ? 'border-primary-500 bg-primary-50/50 dark:bg-primary-900/10' : 'border-gray-200 dark:border-dark-600'"><input v-model="form.buyer_type" type="radio" value="company" name="buyer_type" class="mt-1 text-primary-600 focus:ring-primary-500" /><span><span class="font-medium">{{ t('invoice.company') }}</span><span class="mt-1 block text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.companyHint') }}</span></span></label>
          </div></div>
          <div class="grid gap-4 sm:grid-cols-2"><label class="block"><span class="input-label">{{ t('invoice.invoiceTitle') }}</span><input v-model="form.title" data-testid="invoice-title" class="input mt-1" maxlength="200" required :placeholder="t(form.buyer_type === 'personal' ? 'invoice.personalPlaceholder' : 'invoice.companyPlaceholder')" /></label><label class="block"><span class="input-label">{{ t('invoice.taxId') }} <span v-if="form.buyer_type === 'personal'" class="font-normal text-gray-400">({{ t('invoice.optional') }})</span></span><input v-model="form.tax_id" data-testid="invoice-tax-id" class="input mt-1" maxlength="30" pattern="[A-Za-z0-9]*" :required="form.buyer_type === 'company'" /></label></div>
          <label class="block"><span class="input-label">{{ t('invoice.emails') }}</span><input v-model="form.emails" data-testid="invoice-emails" class="input mt-1" maxlength="2600" required :placeholder="t('invoice.emailsPlaceholder')" /><span class="mt-1 block text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.emailsHint') }}</span></label>
          <label class="block"><span class="input-label">{{ t('invoice.remark') }}</span><textarea v-model="form.remark" data-testid="invoice-remark" class="input mt-1" rows="3" maxlength="2000" :placeholder="t('invoice.remarkPlaceholder')" /></label>
        </fieldset>
      </form>
      <template #footer><div class="flex justify-end gap-3"><button type="button" class="btn btn-secondary" :disabled="submitting" @click="closeForm">{{ t('common.cancel') }}</button><button type="submit" form="invoice-request-form" data-testid="invoice-submit" class="btn btn-primary" :disabled="submitting || !canApply">{{ submitting ? t('common.processing') : t('invoice.submit') }}</button></div></template>
    </BaseDialog>

    <BaseDialog :show="!!selectedRequest" :title="t('invoice.detail')" width="wide" @close="selectedRequest = null"><InvoiceRequestDetails v-if="selectedRequest" :request="selectedRequest" /><template #footer><div class="flex justify-end"><button v-if="selectedRequest?.status === 'issued'" class="btn btn-primary" :disabled="downloadingIds.has(selectedRequest.id)" @click="download(selectedRequest)"><Icon name="download" />{{ t('invoice.download') }}</button></div></template></BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { invoiceAPI, saveInvoicePDF } from '@/api/invoices'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { InvoiceConfig, InvoiceOrder, InvoiceRequest } from '@/types/invoice'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import InvoiceRequestDetails from '@/components/payment/InvoiceRequestDetails.vue'

const { t } = useI18n()
const appStore = useAppStore()
const config = ref<InvoiceConfig>({ enabled: false, min_amount: 300, instructions: '', currency: 'CNY' })
const loading = ref(true)
const ordersLoading = ref(false)
const historyLoading = ref(false)
const submitting = ref(false)
const showForm = ref(false)
const orders = ref<InvoiceOrder[]>([])
const requests = ref<InvoiceRequest[]>([])
const availableAmount = ref(0)
const selectedOrders = ref(new Map<number, InvoiceOrder>())
const selectedRequest = ref<InvoiceRequest | null>(null)
const downloadingIds = ref(new Set<number>())
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
const historyPagination = reactive({ page: 1, page_size: 20, total: 0 })
const form = reactive({ buyer_type: 'personal' as 'personal' | 'company', title: '', tax_id: '', emails: '', remark: '' })
let ordersRequestId = 0
let historyRequestId = 0
const selectedCents = computed(() => [...selectedOrders.value.values()].reduce((sum, order) => sum + Math.round(order.pay_amount * 100), 0))
const selectedAmount = computed(() => selectedCents.value / 100)
const canApply = computed(() => config.value.enabled && !loading.value && !ordersLoading.value && !submitting.value && selectedOrders.value.size > 0 && selectedCents.value >= Math.round(config.value.min_amount * 100))
const allPageSelected = computed(() => orders.value.length > 0 && orders.value.every(order => selectedOrders.value.has(order.id)))

function showError(error: unknown) { appStore.showError(extractI18nErrorMessage(error, t, 'invoice.errors', t('invoice.errors.generic'))) }

async function loadOrders() {
  const requestId = ++ordersRequestId
  ordersLoading.value = true
  try {
    const { data } = await invoiceAPI.getOrders({ page: orderPagination.page, page_size: orderPagination.page_size })
    if (requestId !== ordersRequestId) return
    orders.value = data.items || []
    orderPagination.total = data.total || 0
    availableAmount.value = data.available_amount || 0
  } catch (error) { if (requestId === ordersRequestId) showError(error) }
  finally { if (requestId === ordersRequestId) ordersLoading.value = false }
}

async function loadRequests() {
  const requestId = ++historyRequestId
  historyLoading.value = true
  try {
    const { data } = await invoiceAPI.getRequests({ page: historyPagination.page, page_size: historyPagination.page_size })
    if (requestId !== historyRequestId) return
    requests.value = data.items || []
    historyPagination.total = data.total || 0
  } catch (error) { if (requestId === historyRequestId) showError(error) }
  finally { if (requestId === historyRequestId) historyLoading.value = false }
}

async function loadConfig() {
  try { config.value = (await invoiceAPI.getConfig()).data }
  catch (error) { config.value.enabled = false; showError(error) }
}

async function refresh() {
  if (submitting.value) return
  loading.value = true
  try {
    await Promise.all([loadConfig(), loadRequests()])
    if (config.value.enabled) await loadOrders()
    else selectedOrders.value = new Map()
  } finally { loading.value = false }
}

function toggleOrder(order: InvoiceOrder) {
  if (submitting.value) return
  const next = new Map(selectedOrders.value)
  if (next.has(order.id)) next.delete(order.id)
  else if (next.size >= 500) { appStore.showError(t('invoice.errors.limit')); return }
  else next.set(order.id, order)
  selectedOrders.value = next
}

function togglePage() {
  if (submitting.value || ordersLoading.value) return
  const next = new Map(selectedOrders.value)
  if (allPageSelected.value) orders.value.forEach(order => next.delete(order.id))
  else {
    const newOrders = orders.value.filter(order => !next.has(order.id))
    if (next.size + newOrders.length > 500) { appStore.showError(t('invoice.errors.limit')); return }
    newOrders.forEach(order => next.set(order.id, order))
  }
  selectedOrders.value = next
}

function changeOrderPage(page: number) { if (submitting.value) return; orderPagination.page = page; loadOrders() }
function changeOrderPageSize(size: number) { if (submitting.value) return; orderPagination.page_size = size; orderPagination.page = 1; loadOrders() }
function changeHistoryPage(page: number) { historyPagination.page = page; loadRequests() }
function changeHistoryPageSize(size: number) { historyPagination.page_size = size; historyPagination.page = 1; loadRequests() }
function openForm() { if (canApply.value) showForm.value = true }
function closeForm() { if (!submitting.value) showForm.value = false }

async function submitRequest() {
  if (submitting.value || !canApply.value) return
  const emails = [...new Set(form.emails.split(/[,，;\s]+/).map(email => email.trim()).filter(Boolean))]
  const taxId = form.tax_id.trim().toUpperCase()
  if (!form.title.trim()) { appStore.showError(t('invoice.errors.requiredTitle')); return }
  if (form.buyer_type === 'company' && !taxId) { appStore.showError(t('invoice.errors.requiredTaxId')); return }
  if (!/^[A-Z0-9]{0,30}$/.test(taxId)) { appStore.showError(t('invoice.errors.invalidTaxId')); return }
  if (!emails.length || emails.length > 10 || emails.some(email => !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email))) { appStore.showError(t('invoice.errors.invalidEmails')); return }
  submitting.value = true
  try {
    await invoiceAPI.createRequest({ order_ids: [...selectedOrders.value.keys()], buyer_type: form.buyer_type, title: form.title.trim(), tax_id: taxId, emails, remark: form.remark.trim() })
    appStore.showSuccess(t('invoice.submitSuccess'))
    showForm.value = false
    selectedOrders.value = new Map()
    Object.assign(form, { buyer_type: 'personal', title: '', tax_id: '', emails: '', remark: '' })
    orderPagination.page = 1
    historyPagination.page = 1
    await Promise.all([loadOrders(), loadRequests()])
  } catch (error) { showError(error) }
  finally { submitting.value = false }
}

async function download(request: InvoiceRequest) {
  if (downloadingIds.value.has(request.id)) return
  downloadingIds.value = new Set(downloadingIds.value).add(request.id)
  try { saveInvoicePDF((await invoiceAPI.downloadPDF(request.id)).data, request) }
  catch (error) { showError(error) }
  finally { const next = new Set(downloadingIds.value); next.delete(request.id); downloadingIds.value = next }
}

onMounted(refresh)
</script>
