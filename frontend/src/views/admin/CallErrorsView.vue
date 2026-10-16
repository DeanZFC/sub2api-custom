<template>
  <AppLayout>
    <div class="space-y-6">
      <p class="text-sm leading-6 text-gray-500 dark:text-gray-400">
        {{ t('usage.errors.pageHelp') }}
      </p>

      <div class="card">
        <div class="flex flex-wrap items-center gap-3 border-b border-gray-100 p-4 dark:border-dark-700/50 sm:px-6">
          <span class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.dashboard.timeRange') }}
          </span>
          <DateRangePicker
            v-model:start-date="startDate"
            v-model:end-date="endDate"
            @change="applyFilters"
          />
        </div>

        <UsageFilters
          v-model="filters"
          flat
          mode="errors"
          :show-model-filter="false"
          :start-date="startDate"
          :end-date="endDate"
          :exporting="false"
          @change="applyFilters"
          @refresh="loadErrors"
          @reset="resetFilters"
        />

        <form
          class="flex flex-wrap items-end gap-4 border-b border-gray-100 px-4 pb-4 dark:border-dark-700/50 sm:px-6 sm:pb-6"
          @submit.prevent="applyFilters"
        >
          <div class="w-full sm:w-64">
            <label for="call-error-model" class="input-label">{{ t('usage.model') }}</label>
            <input
              id="call-error-model"
              v-model="filters.model"
              type="search"
              class="input"
              :placeholder="t('usage.errors.modelExactPlaceholder')"
            />
          </div>
          <div class="min-w-0 flex-1 basis-64">
            <label for="call-error-query" class="input-label">{{ t('usage.errors.searchLabel') }}</label>
            <input
              id="call-error-query"
              v-model="filters.q"
              type="search"
              class="input"
              :placeholder="t('usage.errors.searchPlaceholder')"
            />
          </div>
          <button type="submit" class="btn btn-primary">{{ t('common.search') }}</button>
        </form>

        <div
          v-if="loadError"
          role="alert"
          class="m-4 flex flex-wrap items-center justify-between gap-3 rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300 sm:m-6"
        >
          <span class="min-w-0 break-words">{{ loadError }}</span>
          <button type="button" class="btn btn-secondary" @click="loadErrors">{{ t('common.retry') }}</button>
        </div>

        <OpsErrorLogTable
          v-show="!loadError"
          flat
          summary-first
          :visible-column-keys="['created_at', 'message', 'user', 'api_key', 'group', 'status', 'actions']"
          :rows="rows"
          :total="total"
          :loading="loading"
          :page="page"
          :page-size="pageSize"
          @open-error-detail="openError"
          @sort="onSort"
          @update:page="onPageChange"
          @update:page-size="onPageSizeChange"
        />
      </div>

      <OpsErrorDetailModal
        v-model:show="showErrorModal"
        :error-id="selectedErrorId"
        error-type="request"
      />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import OpsErrorLogTable from './ops/components/OpsErrorLogTable.vue'
import OpsErrorDetailModal from './ops/components/OpsErrorDetailModal.vue'
import { listErrorLogs, type OpsErrorLog } from '@/api/admin/ops'

interface ErrorFilters {
  user_id?: number | null
  api_key_id?: number | null
  account_id?: number | null
  group_id?: number | null
  model?: string
  q?: string
  error_phase?: string | null
  error_category?: string | null
  status_code?: number | null
}

const { t } = useI18n()

function formatLocalDate(date: Date): string {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

function defaultDateRange() {
  const end = new Date()
  const start = new Date(end)
  start.setDate(start.getDate() - 6)
  return { start: formatLocalDate(start), end: formatLocalDate(end) }
}

const initialRange = defaultDateRange()
const startDate = ref(initialRange.start)
const endDate = ref(initialRange.end)
function defaultFilters(): ErrorFilters {
  return { group_id: null, error_phase: null, error_category: null, status_code: null, model: '', q: '' }
}

const filters = ref<ErrorFilters>(defaultFilters())
const rows = ref<OpsErrorLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const sortBy = ref('created_at')
const sortOrder = ref<'asc' | 'desc'>('desc')
const loading = ref(false)
const loadError = ref('')
const showErrorModal = ref(false)
const selectedErrorId = ref<number | null>(null)
let requestSequence = 0

function parseLocalDate(value: string): Date | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return null
  const date = new Date(`${value}T00:00:00`)
  return Number.isFinite(date.getTime()) && formatLocalDate(date) === value ? date : null
}

async function loadErrors() {
  const sequence = ++requestSequence
  loadError.value = ''
  rows.value = []
  total.value = 0

  const start = parseLocalDate(startDate.value)
  const end = parseLocalDate(endDate.value)
  if (!start || !end || start > end) {
    loading.value = false
    loadError.value = t('usage.errors.invalidDateRange')
    return
  }
  // The API uses an exclusive end time; include the entire selected last day, including DST changes.
  end.setDate(end.getDate() + 1)
  loading.value = true

  try {
    const response = await listErrorLogs({
      page: page.value,
      page_size: pageSize.value,
      view: 'all',
      start_time: start.toISOString(),
      end_time: end.toISOString(),
      user_id: filters.value.user_id ?? undefined,
      api_key_id: filters.value.api_key_id ?? undefined,
      account_id: filters.value.account_id ?? undefined,
      group_id: filters.value.group_id ?? undefined,
      model: filters.value.model?.trim() || undefined,
      q: filters.value.q?.trim() || undefined,
      phase: filters.value.error_phase || undefined,
      category: filters.value.error_category || undefined,
      status_codes: filters.value.status_code != null ? String(filters.value.status_code) : undefined,
      sort_by: sortBy.value,
      sort_order: sortOrder.value,
    })
    if (sequence !== requestSequence) return
    rows.value = response.items ?? []
    total.value = response.total
  } catch (error: unknown) {
    if (sequence !== requestSequence) return
    if (error && typeof error === 'object' && 'code' in error && error.code === 'OPS_DISABLED') {
      loadError.value = t('usage.errors.monitoringDisabled')
      return
    }
    const message = error && typeof error === 'object' && 'message' in error && typeof error.message === 'string'
      ? error.message
      : ''
    loadError.value = message
      ? `${t('usage.errors.failedToLoad')}: ${message}`
      : t('usage.errors.failedToLoad')
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  void loadErrors()
}

function resetFilters() {
  const range = defaultDateRange()
  startDate.value = range.start
  endDate.value = range.end
  filters.value = defaultFilters()
  applyFilters()
}

function onSort(key: string, order: 'asc' | 'desc') {
  sortBy.value = key
  sortOrder.value = order
  applyFilters()
}

function onPageChange(value: number) {
  page.value = value
  void loadErrors()
}

function onPageSizeChange(value: number) {
  pageSize.value = value
  applyFilters()
}

function openError(id: number) {
  selectedErrorId.value = id
  showErrorModal.value = true
}

onMounted(loadErrors)
onBeforeUnmount(() => { requestSequence += 1 })
</script>
