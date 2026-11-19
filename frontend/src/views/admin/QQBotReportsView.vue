<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <header class="flex flex-wrap items-start justify-between gap-3">
        <div><h1 class="text-2xl font-semibold">{{ t('qqbot.reports.title') }}</h1><p class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('qqbot.reports.help') }}</p></div>
        <button type="button" class="btn btn-secondary" :disabled="loading" data-testid="report-refresh" @click="load(report?.page || 1)">{{ t('common.refresh') }}</button>
      </header>
      <section class="card space-y-4 p-4 sm:p-5">
        <div class="flex flex-wrap gap-2" :aria-label="t('qqbot.reports.quickDates')">
          <button v-for="preset in presets" :key="preset" type="button" class="btn btn-secondary" :data-testid="`report-preset-${preset}`" @click="selectPreset(preset)">{{ t(`qqbot.reports.presets.${preset}`) }}</button>
        </div>
        <form class="grid items-end gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="report-form" @submit.prevent="search">
          <label class="space-y-1 text-sm">{{ t('qqbot.reports.from') }}<input v-model="filters.start_date" type="date" class="input" required data-testid="report-start" /></label>
          <label class="space-y-1 text-sm">{{ t('qqbot.reports.to') }}<input v-model="filters.end_date" type="date" class="input" required data-testid="report-end" /></label>
          <label class="space-y-1 text-sm">{{ t('qqbot.reports.source') }}<select v-model="filters.kind" class="input" data-testid="report-kind"><option value="">{{ t('qqbot.workspace.all') }}</option><option v-for="kind in kinds" :key="kind" :value="kind">{{ t(`qqbot.reports.kinds.${kind}`) }}</option></select></label>
          <label class="space-y-1 text-sm">{{ t('qqbot.reports.rewardType') }}<select v-model="filters.reward_type" class="input" data-testid="report-type"><option value="">{{ t('qqbot.workspace.all') }}</option><option v-for="type in rewardTypes" :key="type" :value="type">{{ t(`qqbot.rewardEditor.types.${type}`) }}</option></select></label>
          <label class="space-y-1 text-sm">{{ t('qqbot.reports.groupFilter') }}<input v-model.trim="filters.group_id" class="input" maxlength="128" :placeholder="t('qqbot.reports.allGroups')" data-testid="report-group" /></label>
          <label class="space-y-1 text-sm sm:col-span-2">{{ t('qqbot.reports.search') }}<input v-model="filters.search" class="input" maxlength="254" data-testid="report-search" /></label>
          <button type="submit" class="btn btn-primary" data-testid="report-submit">{{ t('common.search') }}</button>
        </form>
        <p v-if="validationError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ validationError }}</p>
        <details class="text-xs text-gray-500 dark:text-gray-400"><summary class="cursor-pointer">{{ t('qqbot.reports.methodTitle') }}</summary><p class="mt-2 leading-6">{{ t('qqbot.reports.method') }}</p></details>
      </section>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
      <p v-if="loading" role="status" class="py-10 text-center text-gray-500">{{ t('common.loading') }}</p>
      <template v-else-if="report">
        <div class="flex flex-wrap items-center justify-between gap-3 text-sm text-gray-500 dark:text-gray-400">
          <p data-testid="report-period">{{ report.start_date }} — {{ report.end_date }} · {{ t('qqbot.reports.beijing') }}</p>
          <button type="button" class="btn btn-secondary" data-testid="report-export" @click="exportDaily">{{ t('qqbot.reports.exportDaily') }}</button>
        </div>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4" data-testid="report-summary">
          <article v-for="stat in stats" :key="stat.key" class="card p-5"><p class="text-sm text-gray-500 dark:text-gray-400">{{ t(`qqbot.reports.metrics.${stat.key}`) }}</p><p class="mt-2 break-all text-2xl font-semibold tabular-nums">{{ stat.value }}</p></article>
        </div>
        <p v-if="!report.total" class="card p-6 text-center text-sm text-gray-500" data-testid="report-empty">{{ t('qqbot.reports.empty') }}</p>
        <section class="card space-y-4 p-4 sm:p-5" :aria-label="t('qqbot.reports.trend')">
          <h2 class="text-lg font-semibold">{{ t('qqbot.reports.trend') }}</h2>
          <div class="h-64"><Line :data="chartData" :options="chartOptions" :aria-label="t('qqbot.reports.trend')" role="img" /></div>
          <details>
            <summary class="cursor-pointer text-sm font-medium">{{ t('qqbot.reports.dailyTable') }}</summary>
            <div class="mt-4 max-h-96 overflow-auto"><table class="w-full min-w-[760px] text-left text-sm"><thead><tr class="text-xs text-gray-500"><th v-for="header in dailyHeaders" :key="header" class="whitespace-nowrap px-3 pb-3">{{ header }}</th></tr></thead><tbody><tr v-for="row in report.daily" :key="row.date" class="border-t dark:border-dark-700"><td class="whitespace-nowrap px-3 py-3">{{ row.date }}</td><td class="px-3 py-3 tabular-nums">${{ rewardReportAmount(row.balance_amount) }}</td><td v-for="key in countKeys" :key="key" class="px-3 py-3 tabular-nums">{{ row[key] }}</td></tr></tbody></table></div>
          </details>
        </section>
        <div class="grid items-start gap-6 xl:grid-cols-2">
          <section class="card min-w-0 space-y-4 p-4 sm:p-5">
            <h2 class="text-lg font-semibold">{{ t('qqbot.reports.bySource') }}</h2>
            <div class="overflow-x-auto"><table class="w-full min-w-[620px] text-left text-sm"><thead><tr class="text-xs text-gray-500"><th class="pb-3 pr-4">{{ t('qqbot.reports.source') }}</th><th class="pb-3 pr-4">{{ t('qqbot.reports.metrics.balance_amount') }}</th><th v-for="key in sourceCountKeys" :key="key" class="pb-3 pr-4">{{ t(`qqbot.reports.metrics.${key}`) }}</th></tr></thead><tbody><tr v-for="row in report.by_kind" :key="row.kind" class="border-t dark:border-dark-700"><td class="whitespace-nowrap py-3 pr-4">{{ t(`qqbot.reports.kinds.${row.kind}`) }}</td><td class="py-3 pr-4 tabular-nums">${{ rewardReportAmount(row.balance_amount) }}</td><td v-for="key in sourceCountKeys" :key="key" class="py-3 pr-4 tabular-nums">{{ row[key] }}</td></tr></tbody></table></div>
            <p v-if="!report.by_kind.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
          </section>
          <section class="card min-w-0 space-y-4 p-4 sm:p-5">
            <div><h2 class="text-lg font-semibold">{{ t('qqbot.reports.topUsers') }}</h2><p class="mt-1 text-xs text-gray-500">{{ t('qqbot.reports.topHelp') }}</p></div>
            <div class="overflow-x-auto"><table class="w-full min-w-[400px] text-left text-sm"><thead><tr class="text-xs text-gray-500"><th class="pb-3 pr-4">{{ t('qqbot.reports.account') }}</th><th class="pb-3 pr-4">{{ t('qqbot.reports.metrics.balance_amount') }}</th><th class="pb-3">{{ t('qqbot.reports.metrics.reward_count') }}</th></tr></thead><tbody><tr v-for="row in report.top_users" :key="row.user_id" class="border-t dark:border-dark-700"><td class="py-3 pr-4"><p class="max-w-60 break-all">{{ row.nickname || row.email || `ID: ${row.user_id}` }}</p><p class="text-xs text-gray-500">ID: {{ row.user_id }}<span v-if="row.nickname && row.email"> · {{ row.email }}</span></p></td><td class="py-3 pr-4 tabular-nums">${{ rewardReportAmount(row.balance_amount) }}</td><td class="py-3 tabular-nums">{{ row.reward_count }}</td></tr></tbody></table></div>
            <p v-if="!report.top_users.length" class="text-sm text-gray-500">{{ t('common.noData') }}</p>
          </section>
        </div>
        <section class="card space-y-4 p-4 sm:p-5" data-testid="report-records">
          <h2 class="text-lg font-semibold">{{ t('qqbot.reports.details') }}</h2>
          <div class="overflow-x-auto"><table class="w-full min-w-[960px] text-left text-sm"><thead><tr class="text-xs text-gray-500"><th v-for="key in ['account', 'source', 'reward', 'status', 'groupFilter', 'claimedAt']" :key="key" class="pb-3 pr-5">{{ t(`qqbot.reports.${key}`) }}</th></tr></thead>
            <tbody><tr v-for="row in report.items" :key="row.id" class="border-t align-top dark:border-dark-700"><td class="py-4 pr-5"><p class="max-w-48 break-all font-medium">{{ row.nickname || row.email || `ID: ${row.user_id}` }}</p><p v-if="row.nickname && row.email" class="max-w-48 break-all text-xs text-gray-500">{{ row.email }}</p><p class="text-xs text-gray-500">ID: {{ row.user_id }}</p><p class="mt-1 max-w-48 break-all text-xs text-gray-500">{{ row.member_id }}</p></td><td class="whitespace-nowrap py-4 pr-5">{{ t(`qqbot.reports.kinds.${row.kind}`) }}</td><td class="py-4 pr-5"><p class="font-medium tabular-nums">{{ row.reward_type === 'balance' ? `$${rewardReportAmount(row.amount)}` : `${t(`qqbot.rewardEditor.types.${row.reward_type}`)} × ${row.quantity}` }}</p><p v-if="row.prize_name" class="mt-1 max-w-48 break-words text-xs text-gray-500">{{ row.prize_name }}</p></td><td class="py-4 pr-5"><span class="inline-block whitespace-nowrap rounded-md px-2 py-1 text-xs" :class="row.status === 'pending' ? 'bg-amber-50 text-amber-800 dark:bg-amber-950/40 dark:text-amber-300' : 'bg-green-50 text-green-800 dark:bg-green-950/40 dark:text-green-300'">{{ t(`qqbot.reports.statuses.${row.status === 'pending' ? 'pending' : ['physical', 'other'].includes(row.reward_type) ? 'fulfilled' : 'credited'}`) }}</span><p v-if="row.fulfilled_at && ['physical', 'other'].includes(row.reward_type)" class="mt-2 whitespace-nowrap text-xs text-gray-500">{{ beijingTime(row.fulfilled_at) }}</p></td><td class="py-4 pr-5"><code class="block max-w-36 break-all text-xs text-gray-500">{{ row.group_id || '—' }}</code></td><td class="whitespace-nowrap py-4 text-xs text-gray-500">{{ beijingTime(row.created_at) }}</td></tr></tbody>
          </table></div>
          <p v-if="!report.items.length" class="py-6 text-center text-sm text-gray-500">{{ t('common.noData') }}</p>
          <div class="flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm dark:border-dark-700"><span class="text-gray-500">{{ t('qqbot.recordsPage', { total: report.total, page: report.page, pages }) }}</span><div class="flex gap-2"><button type="button" class="btn btn-secondary" :disabled="loading || report.page <= 1" data-testid="report-previous" @click="load(report.page - 1)">{{ t('qqbot.previousPage') }}</button><button type="button" class="btn btn-secondary" :disabled="loading || report.page >= pages" data-testid="report-next" @click="load(report.page + 1)">{{ t('qqbot.nextPage') }}</button></div></div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend } from 'chart.js'
import { Line } from 'vue-chartjs'
import AppLayout from '@/components/layout/AppLayout.vue'
import { qqbotAPI, type QQRewardReport, type QQRewardReportFilter, type QQRewardReportKind, type QQRewardReportType } from '@/api/qqbot'
import { rewardReportAmount, rewardReportDates, rewardReportDailyCSV, validRewardReportDates, type RewardReportPreset } from '@/components/admin/qqbot/rewardReports'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend)
const { t, locale } = useI18n()
const presets: RewardReportPreset[] = ['today', 'yesterday', 'last7', 'last30', 'month']
const kinds: QQRewardReportKind[] = ['checkin', 'invite', 'lottery', 'growth', 'streak', 'ranking', 'redemption']
const rewardTypes: QQRewardReportType[] = ['balance', 'chances', 'coupon', 'physical', 'other']
const filters = reactive<QQRewardReportFilter>({ ...rewardReportDates('last7'), group_id: '', kind: '', reward_type: '', search: '' })
let applied: QQRewardReportFilter = { ...filters }
const report = ref<QQRewardReport | null>(null), loading = ref(false), error = ref(''), validationError = ref('')
const countKeys = ['user_count', 'reward_count', 'chances_quantity', 'coupon_quantity', 'pending_quantity', 'fulfilled_quantity'] as const
const sourceCountKeys = ['reward_count', 'user_count', 'chances_quantity', 'coupon_quantity', 'pending_quantity', 'fulfilled_quantity'] as const
const pages = computed(() => Math.max(1, Math.ceil((report.value?.total || 0) / (report.value?.page_size || 20))))
const stats = computed(() => report.value ? [
  { key: 'balance_amount', value: `$${rewardReportAmount(report.value.summary.balance_amount)}` },
  ...countKeys.map(key => ({ key, value: report.value!.summary[key].toLocaleString(locale.value) }))
] : [])
const dailyHeaders = computed(() => [t('qqbot.reports.date'), ...['balance_amount', ...countKeys].map(key => t(`qqbot.reports.metrics.${key}`))])
const chartData = computed(() => ({ labels: report.value?.daily.map(row => row.date) || [], datasets: [
  { label: t('qqbot.reports.metrics.balance_amount'), data: report.value?.daily.map(row => row.balance_amount) || [], borderColor: '#6366f1', backgroundColor: '#6366f1', pointRadius: 2, yAxisID: 'y' },
  { label: t('qqbot.reports.metrics.user_count'), data: report.value?.daily.map(row => row.user_count) || [], borderColor: '#10b981', backgroundColor: '#10b981', pointRadius: 2, yAxisID: 'y1' }
] }))
const chartOptions = computed(() => ({ responsive: true, maintainAspectRatio: false, interaction: { mode: 'index' as const, intersect: false }, scales: {
  y: { beginAtZero: true, title: { display: true, text: 'USD' } },
  y1: { beginAtZero: true, position: 'right' as const, grid: { drawOnChartArea: false }, ticks: { precision: 0 }, title: { display: true, text: t('qqbot.reports.metrics.user_count') } }
}, plugins: { legend: { position: 'bottom' as const } } }))
function beijingTime(value: string) { return new Date(value).toLocaleString(locale.value === 'zh' ? 'zh-CN' : 'en-GB', { timeZone: 'Asia/Shanghai', hour12: false }) }
function selectPreset(preset: RewardReportPreset) { Object.assign(filters, rewardReportDates(preset)); search() }
function search() {
  if (!validRewardReportDates(filters.start_date, filters.end_date)) { validationError.value = t('qqbot.reports.invalidDates'); return }
  validationError.value = ''
  applied = { ...filters, group_id: filters.group_id.trim(), search: filters.search.trim() }
  void load(1)
}
let request = 0
async function load(page: number) {
  const seq = ++request
  loading.value = true; error.value = ''; report.value = null
  try {
    let result = await qqbotAPI.reports({ ...applied, page, page_size: 20 })
    const lastPage = Math.max(1, Math.ceil(result.total / result.page_size))
    if (seq !== request) return
    if (page > lastPage) result = await qqbotAPI.reports({ ...applied, page: lastPage, page_size: 20 })
    if (seq === request) report.value = result
  } catch (e) { if (seq === request) error.value = (e as Error).message || t('common.error') }
  finally { if (seq === request) loading.value = false }
}
function exportDaily() {
  if (!report.value) return
  const url = URL.createObjectURL(new Blob([rewardReportDailyCSV(report.value, dailyHeaders.value)], { type: 'text/csv;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url; link.download = `qq-rewards-daily-${report.value.start_date}-${report.value.end_date}.csv`
  document.body.appendChild(link); link.click(); link.remove(); URL.revokeObjectURL(url)
}
onBeforeUnmount(() => { ++request })
void load(1)
</script>
