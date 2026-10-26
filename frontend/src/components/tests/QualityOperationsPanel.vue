<template>
  <div class="space-y-5" data-quality-operations>
    <PassiveQualityRuntimePanel :visible="visible" />
    <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
    <p v-if="loading" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
    <template v-if="snapshot && config">
      <div class="grid gap-3 sm:grid-cols-3">
        <div v-for="metric in ['requests', 'tokens', 'unknown_requests', 'recovery_requests', 'recovery_tokens'] as const" :key="metric" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><p class="text-sm text-gray-500">{{ tr(metric) }}</p><strong class="mt-2 block text-2xl">{{ (total?.[metric] || 0).toLocaleString() }}</strong></div>
      </div>
      <p class="text-xs text-gray-500">{{ tr('resetHint', { date: date(snapshot.resets_at) }) }}</p>
      <form class="space-y-5 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-900" @submit.prevent="save">
        <h3 class="font-semibold">{{ tr('budgetTitle') }}</h3><p class="text-sm text-gray-500">{{ tr('budgetHint') }}</p>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.budget_enabled" type="checkbox" />{{ tr('enableBudget') }}</label>
        <fieldset :disabled="!config.budget_enabled && !budgetError" v-show="config.budget_enabled || !!budgetError" class="space-y-4" data-budget-fields>
          <div class="grid gap-4 sm:grid-cols-2"><label v-for="f in budgetFields.slice(0, 2)" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></div>
          <details :open="!!budgetError" class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"><summary class="cursor-pointer text-sm">{{ t('admin.tests.experience.budgetAdvanced') }}</summary><div class="mt-3 grid gap-4 sm:grid-cols-3"><label v-for="f in budgetFields.slice(2)" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></div></details>
        </fieldset>
        <p v-if="!config.budget_enabled" class="text-sm text-gray-500">{{ t('admin.tests.experience.budgetOff') }}</p>
        <p v-if="config.budget_enabled" class="text-xs text-amber-700 dark:text-amber-400">{{ tr('tokenHint') }}</p><p v-if="config.budget_enabled" class="text-xs text-gray-500">{{ tr('recoveryReserveHint') }}</p>
        <hr class="dark:border-dark-700" /><h3 class="font-semibold">{{ tr('frequencyTitle') }}</h3>
        <label class="flex items-center gap-2 text-sm"><input v-model="config.dynamic_enabled" type="checkbox" />{{ tr('enableDynamic') }}</label><p class="text-sm text-gray-500">{{ tr('frequencyHint') }}</p>
        <fieldset :disabled="!config.dynamic_enabled && !frequencyError" v-show="config.dynamic_enabled || !!frequencyError" data-frequency-fields class="grid gap-4 sm:grid-cols-3"><label v-for="f in frequencyFields" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="config[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></fieldset>
        <p v-if="!config.dynamic_enabled" class="text-sm text-gray-500">{{ t('admin.tests.experience.dynamicOff') }}</p>
        <p v-if="budgetError || frequencyError" role="alert" class="text-sm text-red-600">{{ budgetError || frequencyError }}</p>
        <div class="flex items-center gap-3"><button class="btn btn-primary" type="submit" :disabled="busy || !!budgetError || !!frequencyError" data-save-operations>{{ t('common.save') }}</button><span v-if="saved" role="status" class="text-sm text-green-600">{{ tr('saved') }}</span></div>
      </form>
      <details class="rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-medium">{{ tr('accountUsage') }}</summary><div class="mt-3 max-h-72 overflow-auto"><table class="w-full text-left text-sm"><thead><tr><th class="p-2">{{ tr('account') }}</th><th class="p-2">{{ tr('requests') }}</th><th class="p-2">{{ tr('tokens') }}</th><th class="p-2">{{ tr('unknown_requests') }}</th><th class="p-2">{{ tr('recovery_requests') }}</th><th class="p-2">{{ tr('recovery_tokens') }}</th></tr></thead><tbody><tr v-for="u in snapshot.usage.filter(u => u.account_id)" :key="u.account_id"><td class="p-2">{{ accountName(u.account_id) }}</td><td class="p-2">{{ u.requests }}</td><td class="p-2">{{ u.tokens }}</td><td class="p-2">{{ u.unknown_requests }}</td><td class="p-2">{{ u.recovery_requests || 0 }}</td><td class="p-2">{{ u.recovery_tokens || 0 }}</td></tr></tbody></table></div></details>
      <details :open="!!snapshot.suppliers.length" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700"><summary class="cursor-pointer text-sm font-semibold">{{ t('admin.tests.experience.suppliersOptional') }}</summary>
        <div class="flex items-center justify-between gap-3"><h3 class="font-semibold">{{ tr('suppliers') }}</h3><button class="btn btn-secondary" :disabled="busy" data-create-supplier @click="editSupplier()">{{ tr('addSupplier') }}</button></div><p class="text-sm text-gray-500">{{ tr('supplierHint') }}</p>
        <p v-if="!snapshot.suppliers.length" class="rounded-xl border border-dashed p-6 text-center text-gray-500">{{ tr('noSuppliers') }}</p>
        <article v-for="s in snapshot.suppliers" :key="s.id" class="space-y-3 rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900">
          <div class="flex flex-wrap items-center justify-between gap-3"><div class="flex items-center gap-2"><strong>{{ s.name }}</strong><span class="badge" :class="s.incident ? 'badge-warning' : 'badge-gray'">{{ tr(!s.enabled ? 'disabled' : s.incident ? 'incident' : s.observed < s.min_accounts ? 'insufficient' : 'noIncident') }}</span></div><div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="busy" @click="editSupplier(s)">{{ t('common.edit') }}</button><button class="btn btn-secondary btn-sm text-red-600" :disabled="busy" @click="deleting = s">{{ t('common.delete') }}</button></div></div>
          <p class="text-sm text-gray-500">{{ tr('supplierSummary', { bad: s.affected, observed: s.observed, total: s.account_ids.length, seconds: s.window_seconds }) }}<span v-if="s.incident && s.next_probe_at"> · {{ tr('nextProbe') }} {{ date(s.next_probe_at) }}</span></p>
          <div class="flex flex-wrap gap-2"><span v-for="m in s.members" :key="m.account_id" class="rounded border border-gray-200 px-2 py-1 text-xs dark:border-dark-700" :title="date(m.observed_at || undefined)">{{ m.name }} · {{ tr(`outcomes.${m.kind || 'unknown'}`) }}</span></div>
        </article>
      </details>
    </template>
    <BaseDialog :show="!!editing" :title="tr('suppliers')" width="wide" @close="editing = null"><form v-if="editing" id="quality-supplier-form" class="space-y-4" @submit.prevent="saveSupplier"><p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><label class="input-label">{{ tr('supplierName') }}<input v-model.trim="editing.name" required maxlength="100" class="input mt-1 w-full" /></label><label class="flex items-center gap-2 text-sm"><input v-model="editing.enabled" type="checkbox" />{{ tr('enableSupplier') }}</label><div class="grid gap-3 sm:grid-cols-2"><label v-for="f in supplierFields" :key="f.key" class="input-label">{{ tr(f.key) }}<input v-model.number="editing[f.key]" type="number" :min="f.min" :max="f.max" step="1" required class="input mt-1 w-full" /></label></div><label class="input-label">{{ tr('members') }} ({{ editing.account_ids.length }})<input v-model="search" type="search" class="input my-2 w-full" :placeholder="tr('searchAccounts')" /></label><div class="max-h-52 space-y-2 overflow-auto"><label v-for="a in choices" :key="a.id" class="flex items-center gap-2 text-sm"><input v-model="editing.account_ids" type="checkbox" :value="a.id" :disabled="assignedElsewhere(a.id)" />{{ a.name }} #{{ a.id }}<span v-if="assignedElsewhere(a.id)" class="text-xs text-gray-500">{{ tr('assigned') }}</span></label></div><p class="text-xs text-gray-500">{{ tr('membershipHint') }}</p></form><template #footer><button class="btn btn-secondary" @click="editing = null">{{ t('common.cancel') }}</button><button type="submit" form="quality-supplier-form" class="btn btn-primary" :disabled="busy">{{ t('common.save') }}</button></template></BaseDialog>
    <BaseDialog :show="!!deleting" :title="t('common.delete')" @close="deleting = null"><p>{{ tr('deleteHint', { name: deleting?.name || '' }) }}</p><p v-if="error" role="alert" class="mt-3 text-sm text-red-600">{{ error }}</p><template #footer><button class="btn btn-secondary" @click="deleting = null">{{ t('common.cancel') }}</button><button class="btn btn-primary" :disabled="busy" @click="removeSupplier">{{ t('common.confirm') }}</button></template></BaseDialog>
  </div>
</template>
<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PassiveQualityRuntimePanel from './PassiveQualityRuntimePanel.vue'
import { qualityOperationsAPI as api, defaultQualitySupplier, type QualityOperationsSnapshot, type QualityOperationsConfig, type QualitySupplier } from '@/api/admin/qualityOperations'
import type { AccountListItem } from '@/types'
import { useQualityLabels } from '@/composables/useQualityLabels'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ visible: boolean; accounts: AccountListItem[] }>()
const { t } = useI18n(), { date } = useQualityLabels()
const tr = (key: string, args: Record<string, unknown> = {}) => t(`admin.tests.operations.${key}`, args)
const snapshot = ref<QualityOperationsSnapshot | null>(null), config = ref<QualityOperationsConfig | null>(null)
const loading = ref(false), busy = ref(false), error = ref(''), saved = ref(false)
const editing = ref<QualitySupplier | null>(null), deleting = ref<QualitySupplier | null>(null), search = ref('')
const total = computed(() => snapshot.value?.usage.find(u => u.account_id === 0))
const budgetFields = [{ key: 'daily_requests', min: 0, max: 10000000 }, { key: 'daily_tokens', min: 0, max: 1000000000000 }, { key: 'account_daily_requests', min: 0, max: 10000000 }, { key: 'account_daily_tokens', min: 0, max: 1000000000000 }, { key: 'token_reservation', min: 1024, max: 10000000 }, { key: 'recovery_reserve_percent', min: 0, max: 50 }] as const
const frequencyFields = [{ key: 'healthy_rounds', min: 1, max: 100 }, { key: 'healthy_base_seconds', min: 60, max: 86400 }, { key: 'healthy_max_seconds', min: 60, max: 86400 }] as const
function invalidField(fields: readonly { key: keyof QualityOperationsConfig; min: number; max: number }[]) {
  const c = config.value
  if (!c) return ''
  const field = fields.find(f => !Number.isInteger(c[f.key]) || Number(c[f.key]) < f.min || Number(c[f.key]) > f.max)
  return field ? t('admin.tests.experience.invalidSetting', { field: tr(field.key), min: field.min, max: field.max }) : ''
}
const budgetError = computed(() => invalidField(budgetFields))
const frequencyError = computed(() => invalidField(frequencyFields) || (config.value && config.value.healthy_max_seconds < config.value.healthy_base_seconds ? t('admin.tests.experience.frequencyOrder') : ''))
const supplierFields = [{ key: 'window_seconds', min: 60, max: 86400 }, { key: 'min_accounts', min: 2, max: 1000 }, { key: 'failure_percent', min: 1, max: 100 }, { key: 'probe_interval_seconds', min: 15, max: 3600 }] as const
const choices = computed(() => {
  const accounts = new Map(props.accounts.map(a => [a.id, { id: a.id, name: a.name }]))
  for (const m of editing.value?.members || []) if (!accounts.has(m.account_id)) accounts.set(m.account_id, { id: m.account_id, name: m.name })
  return [...accounts.values()].filter(a => `${a.name} ${a.id}`.toLowerCase().includes(search.value.toLowerCase()))
})
function accountName(id: number) { return `${props.accounts.find(a => a.id === id)?.name || ''} #${id}` }
function assignedElsewhere(id: number) { return !!snapshot.value?.suppliers.some(s => s.id !== editing.value?.id && s.account_ids.includes(id)) }
function report(e: unknown) { error.value = extractApiErrorMessage(e, tr('failed')) }
async function refresh() {
  if (loading.value) return
  loading.value = true; error.value = ''
  try { const previous = snapshot.value ? { recovery_reserve_percent: 0, ...snapshot.value.config } : undefined; snapshot.value = await api.get(); if (!config.value || JSON.stringify(config.value) === JSON.stringify(previous)) config.value = { recovery_reserve_percent: 0, ...snapshot.value.config } } catch (e) { report(e) } finally { loading.value = false }
}
async function save() {
  if (!config.value || busy.value || budgetError.value || frequencyError.value) return
  busy.value = true; error.value = ''; saved.value = false
  try { config.value = await api.save({ ...config.value }); if (snapshot.value) snapshot.value.config = { ...config.value }; saved.value = true; await refresh() } catch (e) { report(e) } finally { busy.value = false }
}
function editSupplier(s?: QualitySupplier) { error.value = ''; editing.value = s ? JSON.parse(JSON.stringify(s)) : defaultQualitySupplier(); search.value = '' }
async function saveSupplier() {
  if (!editing.value || busy.value) return
  busy.value = true; error.value = ''
  try { await api.saveSupplier(editing.value); editing.value = null; await refresh() } catch (e) { report(e) } finally { busy.value = false }
}
async function removeSupplier() {
  if (!deleting.value || busy.value) return
  busy.value = true; error.value = ''
  try { await api.deleteSupplier(deleting.value); deleting.value = null; await refresh() } catch (e) { report(e) } finally { busy.value = false }
}
watch(() => props.visible, visible => { if (visible) void refresh(); else { editing.value = null; deleting.value = null } }, { immediate: true })
watch(config, () => { saved.value = false }, { deep: true, flush: 'sync' })
defineExpose({ refresh })
</script>
