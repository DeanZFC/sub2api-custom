<template>
  <section class="space-y-5" data-quality-overview>
    <div class="rounded-xl border border-primary-100 bg-primary-50/60 p-5 dark:border-primary-900 dark:bg-primary-950/20">
      <h3 class="font-semibold text-gray-900 dark:text-white">{{ tr('overview') }}</h3>
      <p class="mt-2 max-w-4xl text-sm leading-6 text-gray-600 dark:text-gray-300">{{ tr('intro') }}</p>
      <div class="mt-4 flex flex-wrap gap-2"><button class="btn btn-secondary btn-sm" @click="$emit('navigate', 'plans')">{{ tr('active') }}</button><button class="btn btn-secondary btn-sm" @click="$emit('navigate', 'passive')">{{ tr('passive') }}</button><button class="btn btn-secondary btn-sm" @click="$emit('navigate', 'types')">{{ tr('questionBank') }}</button></div>
    </div>
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="load(false)">
      <label class="input-label">{{ tr('search') }}<input v-model.trim="search" class="input mt-1" maxlength="200" /></label>
      <label class="input-label">{{ t('admin.tests.target') }}<select v-model.number="group" class="input mt-1"><option :value="0">{{ t('admin.tests.passive.all') }}</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option></select></label>
      <label class="input-label">{{ t('admin.tests.model') }}<input v-model.trim="model" class="input mt-1" maxlength="200" /></label>
      <label class="flex items-center gap-2 py-2 text-sm"><input v-model="problemsOnly" type="checkbox" />{{ tr('problemsOnly') }}</label>
      <button class="btn btn-secondary" :disabled="loading">{{ t('common.refresh') }}</button>
    </form>
    <p class="text-xs text-gray-500">{{ tr('coverageHint') }} · {{ date(updatedAt) }}</p>
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }} <button class="underline" @click="load(false)">{{ t('common.retry') }}</button></p>
    <div class="space-y-3">
      <article v-for="account in items" :key="account.id" class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900" :data-quality-account="account.id">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-800">
          <div><strong>{{ account.name }}</strong><span class="ml-2 text-xs text-gray-500">#{{ account.id }} · {{ account.platform }}</span><p class="mt-1 text-xs text-gray-500">{{ tr('coverage', { active: account.active_plans, passive: account.passive_policies }) }}</p></div>
          <span class="badge" :class="account.controls.some(c => c.blocking) ? 'badge-warning' : 'badge-gray'">{{ tr(account.controls.some(c => c.blocking) ? 'restricted' : account.controls.some(c => c.phase === 'limited') ? 'limited' : 'noHold') }}</span>
        </div>
        <p v-if="!account.controls.length" class="px-5 py-4 text-sm text-gray-500">{{ tr('noEvidence') }}</p>
        <div v-for="(control, index) in account.controls" :key="`${control.source}-${control.id}-${index}`" class="grid gap-3 border-b border-gray-100 px-5 py-4 text-sm last:border-0 dark:border-dark-800 lg:grid-cols-[1fr_2fr_1fr_auto]">
          <div><span class="text-xs text-gray-500">{{ tr(`sources.${control.source}`) }}</span><p class="mt-1 font-medium">{{ control.name && control.source !== 'account' ? control.name : reason(control.reason) }}</p><p class="mt-1 text-xs text-gray-500">{{ control.model || tr('allModels') }} · {{ tr(control.scope === 'account' ? 'accountScope' : 'modelScope') }}</p></div>
          <div><span class="badge" :class="control.blocking ? 'badge-warning' : 'badge-gray'">{{ t(`admin.tests.passive.phases.${control.phase}`) }}</span><p class="mt-2 break-words text-gray-600 dark:text-gray-300">{{ reason(control.reason) || '—' }}</p><p v-if="control.evidence" class="mt-2 text-xs text-gray-500">{{ tr('evidence', { samples: control.evidence.samples, error: metric(control.evidence.error_rate, '%'), ttft: metric(control.evidence.ttft, ' ms'), cache: metric(control.evidence.cache_rate, '%') }) }}</p></div>
          <div class="text-xs text-gray-500"><p>{{ tr(`recovery.${control.recovery}`) }}</p><p class="mt-2">{{ date(control.next_at) }}</p><p class="mt-2">{{ tr('updated') }} {{ date(control.updated_at) }}</p></div>
          <button v-if="control.source !== 'account'" class="btn btn-secondary btn-sm self-start" @click="$emit('navigate', control.source === 'active' ? 'plans' : 'records', control.id, account.id)">{{ tr('inspect') }}</button>
        </div>
      </article>
      <p v-if="!loading && !items.length && !error" class="rounded-xl border border-dashed p-10 text-center text-gray-500">{{ t('common.noData') }}</p>
    </div>
    <button v-if="nextAfter" class="btn btn-secondary" :disabled="loading" @click="load(true)">{{ t('admin.tests.passive.more') }}</button>
  </section>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { channelQualityAPI, type QualityAccount } from '@/api/admin/channelQuality'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
const props = defineProps<{ groups: AdminGroup[]; accountId?: number; visible: boolean }>()
defineEmits<{ navigate: [tab: string, policy?: number, account?: number] }>()
const { t } = useI18n()
const tr = (key: string, values: Record<string, unknown> = {}) => t(`admin.tests.quality.${key}`, values)
const { reason, date } = useQualityLabels()
const search = ref(''), model = ref(''), group = ref(0), problemsOnly = ref(false)
const items = ref<QualityAccount[]>([]), nextAfter = ref(0), loading = ref(false), error = ref(''), updatedAt = ref('')
let request = 0
let paginated = false
let filterKey = ''
async function load(append = false) {
  const key = JSON.stringify([search.value, model.value, group.value, props.accountId, problemsOnly.value])
  append = append && key === filterKey && nextAfter.value > 0
  const token = ++request
  loading.value = true
  try {
    const page = await channelQualityAPI.overview({ search: search.value, model: model.value, group_id: group.value, account_id: props.accountId, problems_only: problemsOnly.value, after: append ? nextAfter.value : undefined })
    if (token !== request) return
    items.value = append ? [...items.value, ...page.items] : page.items
    paginated = append
    filterKey = key
    nextAfter.value = page.next_after || 0
    updatedAt.value = page.generated_at
    error.value = ''
  } catch (e) { if (token === request) error.value = extractApiErrorMessage(e, tr('loadFailed')) }
  finally { if (token === request) loading.value = false }
}
const metric = (value: number | null, suffix: string) => value == null ? '—' : `${Math.round(value * 100) / 100}${suffix}`
watch(() => [props.visible, props.accountId], () => { if (props.visible) void load() })
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => { if (props.visible) void load(); timer = setInterval(() => { if (props.visible && !document.hidden && !loading.value && !paginated) void load() }, 15000) })
onUnmounted(() => { request++; clearInterval(timer) })
defineExpose({ refresh: load })
</script>
