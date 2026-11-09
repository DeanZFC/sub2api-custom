<template>
  <AppLayout>
    <div class="space-y-6">
      <section class="card p-5 sm:p-6">
        <h2 class="text-lg font-semibold">{{ t('invoice.configTitle') }}</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.configHint') }}</p>
        <form class="mt-5 space-y-5" @submit.prevent="saveConfig">
          <fieldset :disabled="configLoading || configSaving" class="space-y-5">
            <div class="flex items-center justify-between gap-4 rounded-xl bg-gray-50 p-4 dark:bg-dark-800"><span class="text-sm font-medium">{{ t('invoice.enabled') }}</span><Toggle v-model="config.enabled" :aria-label="t('invoice.enabled')" /></div>
            <div class="grid gap-5 md:grid-cols-2">
              <label class="block"><span class="input-label">{{ t('invoice.minAmount') }}</span><input v-model.number="config.min_amount" data-testid="invoice-min-amount" type="number" min="0.01" max="1000000000" step="0.01" required class="input mt-1" /><span class="mt-1 block text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('invoice.minAmountHint') }}</span></label>
              <label class="block"><span class="input-label">{{ t('invoice.feeRate') }}</span><input v-model.number="config.fee_rate" data-testid="invoice-fee-rate" type="number" min="0" max="100" step="0.01" required class="input mt-1" /><span class="mt-1 block text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('invoice.feeRateHint') }}</span></label>
              <label class="block"><span class="input-label">{{ t('invoice.feeMinAmount') }}</span><input v-model.number="config.fee_min_amount" data-testid="invoice-fee-min-amount" type="number" min="0" max="1000000000" step="0.01" required class="input mt-1" /><span class="mt-1 block text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('invoice.feeMinAmountHint') }}</span></label>
              <label class="block"><span class="input-label">{{ t('invoice.customInstructions') }}</span><textarea v-model="config.instructions" data-testid="invoice-instructions" class="input mt-1" rows="3" maxlength="5000" :placeholder="t('invoice.instructionsPlaceholder')" /></label>
            </div>
          </fieldset>
          <div class="flex justify-end"><button data-testid="invoice-config-save" class="btn btn-primary" type="submit" :disabled="configLoading || configSaving">{{ configSaving ? t('common.saving') : t('common.save') }}</button></div>
        </form>
      </section>

      <section class="card overflow-hidden">
        <div class="flex flex-wrap items-center justify-between gap-3 p-5"><h2 class="font-semibold">{{ t('invoice.requestsTitle') }}</h2><div class="flex items-center gap-3"><Select v-model="statusFilter" :options="statusOptions" class="w-36" @change="changeStatus" /><button class="btn btn-secondary" :title="t('common.refresh')" :disabled="loading" @click="loadRequests"><Icon name="refresh" :class="loading ? 'animate-spin' : ''" /></button></div></div>
        <div class="overflow-x-auto"><table class="w-full min-w-[960px] text-left text-sm">
          <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr><th class="px-5 py-3 font-medium">{{ t('invoice.requestId') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.applicant') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.invoiceTitle') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.amount') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.status') }}</th><th class="px-4 py-3 font-medium">{{ t('invoice.createdAt') }}</th><th class="px-4 py-3 font-medium">{{ t('common.actions') }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-if="loading"><td colspan="7" class="p-8 text-center text-gray-400">{{ t('common.loading') }}</td></tr>
            <tr v-else-if="!requests.length"><td colspan="7" class="p-10 text-center text-gray-400">{{ t('invoice.noRequests') }}</td></tr>
            <tr v-for="request in loading ? [] : requests" :key="request.id"><td class="px-5 py-4 font-mono">#{{ request.id }}</td><td class="max-w-56 break-all px-4 py-4"><p>{{ request.user_email }}</p><p class="mt-1 text-xs text-gray-400">#{{ request.user_id }}</p></td><td class="max-w-64 break-words px-4 py-4">{{ request.title }}</td><td class="px-4 py-4 font-semibold">¥{{ request.amount.toFixed(2) }}</td><td class="px-4 py-4"><InvoiceStatusBadge :status="request.status" /></td><td class="whitespace-nowrap px-4 py-4 text-gray-500 dark:text-gray-400">{{ formatOrderDateTime(request.created_at) }}</td><td class="px-4 py-4"><div class="flex items-center gap-3 whitespace-nowrap"><button class="text-primary-600 hover:underline dark:text-primary-400" @click="openRequest(request)">{{ t('common.view') }}</button><button v-if="request.status === 'pending'" class="text-primary-600 hover:underline dark:text-primary-400" @click="openRequest(request, 'issue')">{{ t('invoice.issue') }}</button><button v-if="request.status === 'issued'" class="inline-flex items-center gap-1 text-primary-600 disabled:opacity-50 dark:text-primary-400" :disabled="downloadingIds.has(request.id)" @click="download(request)"><Icon name="download" size="sm" />{{ t('invoice.download') }}</button></div></td></tr>
          </tbody>
        </table></div>
        <Pagination v-if="pagination.total > 0" class="p-4" :page="pagination.page" :total="pagination.total" :page-size="pagination.page_size" @update:page="changePage" @update:page-size="changePageSize" />
      </section>
    </div>

    <BaseDialog :show="!!selectedRequest" :title="t(action === 'issue' ? 'invoice.issue' : action === 'reject' ? 'invoice.reject' : 'invoice.detail')" width="wide" :close-on-escape="!actionLoading" :show-close-button="!actionLoading" @close="closeRequest">
      <InvoiceRequestDetails v-if="selectedRequest" :request="selectedRequest" show-user />
      <form v-if="action && selectedRequest?.status === 'pending'" id="invoice-process-form" class="mt-6 space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700" @submit.prevent="processRequest">
        <p class="rounded-xl p-4 text-sm leading-6" :class="action === 'issue' ? 'bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'">{{ t(action === 'issue' ? 'invoice.issueHint' : 'invoice.rejectHint') }}</p>
        <fieldset :disabled="actionLoading" class="space-y-4">
          <template v-if="action === 'issue'">
            <label class="block"><span class="input-label">{{ t('invoice.invoiceNumber') }}</span><input v-model="invoiceNumber" data-testid="invoice-number" class="input mt-1" maxlength="100" required /></label>
            <label class="block"><span class="input-label">{{ t('invoice.pdfFile') }}</span><input data-testid="invoice-file" type="file" accept="application/pdf,.pdf" class="input mt-1 cursor-pointer file:mr-3 file:rounded-lg file:border-0 file:bg-primary-50 file:px-3 file:py-1 file:text-sm file:text-primary-700 dark:file:bg-primary-900/20 dark:file:text-primary-300" required @change="selectFile" /><span class="mt-1 block text-sm text-gray-500 dark:text-gray-400">{{ t('invoice.pdfHint') }}</span></label>
          </template>
          <label class="block"><span class="input-label">{{ t('invoice.adminRemark') }}</span><textarea v-model="adminRemark" data-testid="invoice-admin-remark" class="input mt-1" rows="3" maxlength="2000" :required="action === 'reject'" /></label>
        </fieldset>
      </form>
      <template #footer>
        <div class="flex flex-wrap justify-end gap-3">
          <template v-if="action"><button class="btn btn-secondary" :disabled="actionLoading" @click="action = null">{{ t('common.cancel') }}</button><button type="submit" form="invoice-process-form" class="btn" :class="action === 'reject' ? 'btn-danger' : 'btn-primary'" :disabled="actionLoading || (action === 'issue' && !pdfFile)">{{ actionLoading ? t('common.processing') : t(action === 'issue' ? 'invoice.issueSubmit' : 'invoice.reject') }}</button></template>
          <template v-else-if="selectedRequest?.status === 'pending'"><button class="btn btn-danger" @click="action = 'reject'">{{ t('invoice.reject') }}</button><button class="btn btn-primary" @click="action = 'issue'">{{ t('invoice.issue') }}</button></template>
          <button v-else-if="selectedRequest?.status === 'issued'" class="btn btn-primary" :disabled="downloadingIds.has(selectedRequest.id)" @click="download(selectedRequest)"><Icon name="download" />{{ t('invoice.download') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminInvoiceAPI, saveInvoicePDF } from '@/api/invoices'
import { useAppStore } from '@/stores/app'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { InvoiceConfig, InvoiceRequest, InvoiceStatus } from '@/types/invoice'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import InvoiceStatusBadge from '@/components/payment/InvoiceStatusBadge.vue'
import InvoiceRequestDetails from '@/components/payment/InvoiceRequestDetails.vue'

const { t } = useI18n()
const appStore = useAppStore()
const config = ref<InvoiceConfig>({ enabled: false, min_amount: 300, fee_rate: 0, fee_min_amount: 0, instructions: '', currency: 'CNY' })
const configLoading = ref(true)
const configSaving = ref(false)
const loading = ref(false)
const requests = ref<InvoiceRequest[]>([])
const pagination = reactive({ page: 1, page_size: 20, total: 0 })
const statusFilter = ref('')
const selectedRequest = ref<InvoiceRequest | null>(null)
const action = ref<'issue' | 'reject' | null>(null)
const actionLoading = ref(false)
const invoiceNumber = ref('')
const adminRemark = ref('')
const pdfFile = ref<File | null>(null)
const downloadingIds = ref(new Set<number>())
let listRequestId = 0
const statusOptions = computed(() => [{ value: '', label: t('common.all') }, ...(['pending', 'issued', 'rejected'] as const).map(status => ({ value: status, label: t(`invoice.${status}`) }))])
watch(action, () => { pdfFile.value = null })

function showError(error: unknown) { appStore.showError(extractI18nErrorMessage(error, t, 'invoice.errors', t('invoice.errors.generic'))) }

async function loadConfig() {
  configLoading.value = true
  try { config.value = (await adminInvoiceAPI.getConfig()).data }
  catch (error) { showError(error) }
  finally { configLoading.value = false }
}

async function saveConfig() {
  if (configSaving.value || configLoading.value) return
  const amount = Number(config.value.min_amount)
  if (!Number.isFinite(amount) || amount <= 0 || amount > 1_000_000_000 || Math.abs(amount * 100 - Math.round(amount * 100)) > 0.000001) { appStore.showError(t('invoice.errors.invalidMinAmount')); return }
  const feeRate = Number(config.value.fee_rate)
  if (typeof config.value.fee_rate !== 'number' || !Number.isFinite(feeRate) || feeRate < 0 || feeRate > 100 || Math.abs(feeRate * 100 - Math.round(feeRate * 100)) > 0.000001) { appStore.showError(t('invoice.errors.INVALID_INVOICE_FEE_RATE')); return }
  const feeMinimum = Number(config.value.fee_min_amount)
  if (typeof config.value.fee_min_amount !== 'number' || !Number.isFinite(feeMinimum) || feeMinimum < 0 || feeMinimum > 1_000_000_000 || Math.abs(feeMinimum * 100 - Math.round(feeMinimum * 100)) > 0.000001) { appStore.showError(t('invoice.errors.INVALID_INVOICE_FEE_MIN_AMOUNT')); return }
  configSaving.value = true
  try {
    config.value = (await adminInvoiceAPI.updateConfig({ enabled: config.value.enabled, min_amount: amount, fee_rate: feeRate, fee_min_amount: feeMinimum, instructions: config.value.instructions.trim() })).data
    appStore.showSuccess(t('invoice.saveSuccess'))
  } catch (error) { showError(error) }
  finally { configSaving.value = false }
}

async function loadRequests() {
  const requestId = ++listRequestId
  loading.value = true
  try {
    const { data } = await adminInvoiceAPI.getRequests({ page: pagination.page, page_size: pagination.page_size, status: statusFilter.value ? statusFilter.value as InvoiceStatus : undefined })
    if (requestId !== listRequestId) return
    requests.value = data.items || []
    pagination.total = data.total || 0
  } catch (error) { if (requestId === listRequestId) showError(error) }
  finally { if (requestId === listRequestId) loading.value = false }
}

function changeStatus() { pagination.page = 1; loadRequests() }
function changePage(page: number) { pagination.page = page; loadRequests() }
function changePageSize(size: number) { pagination.page_size = size; pagination.page = 1; loadRequests() }

function openRequest(request: InvoiceRequest, mode: 'issue' | 'reject' | null = null) {
  selectedRequest.value = request
  action.value = request.status === 'pending' ? mode : null
  invoiceNumber.value = ''
  adminRemark.value = ''
  pdfFile.value = null
}

function closeRequest() { if (!actionLoading.value) { selectedRequest.value = null; action.value = null; pdfFile.value = null } }

function selectFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  pdfFile.value = null
  if (!file) return
  if (!file.name.toLowerCase().endsWith('.pdf') || file.size === 0 || file.size > 10 * 1024 * 1024) { appStore.showError(t('invoice.errors.requiredPDF')); input.value = ''; return }
  pdfFile.value = file
}

async function processRequest() {
  const request = selectedRequest.value
  if (!request || request.status !== 'pending' || actionLoading.value || !action.value) return
  const mode = action.value
  if (mode === 'reject' && !adminRemark.value.trim()) { appStore.showError(t('invoice.errors.requiredRejectReason')); return }
  if (mode === 'issue' && !invoiceNumber.value.trim()) { appStore.showError(t('invoice.errors.requiredInvoiceNumber')); return }
  if (mode === 'issue' && !pdfFile.value) { appStore.showError(t('invoice.errors.requiredPDF')); return }
  actionLoading.value = true
  try {
    if (mode === 'issue') {
      const data = new FormData()
      data.append('file', pdfFile.value!)
      data.append('invoice_number', invoiceNumber.value.trim())
      data.append('admin_remark', adminRemark.value.trim())
      selectedRequest.value = (await adminInvoiceAPI.issue(request.id, data)).data
    } else {
      selectedRequest.value = (await adminInvoiceAPI.reject(request.id, { admin_remark: adminRemark.value.trim() })).data
    }
    appStore.showSuccess(t(mode === 'issue' ? 'invoice.issueSuccess' : 'invoice.rejectSuccess'))
    action.value = null
    pdfFile.value = null
    await loadRequests()
  } catch (error) { showError(error) }
  finally { actionLoading.value = false }
}

async function download(request: InvoiceRequest) {
  if (downloadingIds.value.has(request.id)) return
  downloadingIds.value = new Set(downloadingIds.value).add(request.id)
  try { saveInvoicePDF((await adminInvoiceAPI.downloadPDF(request.id)).data, request) }
  catch (error) { showError(error) }
  finally { const next = new Set(downloadingIds.value); next.delete(request.id); downloadingIds.value = next }
}

onMounted(() => { loadConfig(); loadRequests() })
</script>
