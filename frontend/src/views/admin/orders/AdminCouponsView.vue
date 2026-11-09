<template>
  <AppLayout><div class="space-y-6">
    <section class="card space-y-3 p-6"><div class="flex flex-wrap items-center justify-between gap-3"><h2 class="text-lg font-semibold">{{ t('coupon.adminTitle') }}</h2><button class="btn btn-primary" @click="form = blank()">{{ t('coupon.create') }}</button></div><p class="text-sm text-gray-500">{{ t('coupon.rules') }}</p></section>
    <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-red-700 dark:bg-red-950">{{ error }}</p><p v-if="notice" role="status" class="text-green-600">{{ notice }}</p>
    <form v-if="form" class="card space-y-4 p-6" @submit.prevent="save"><h3 class="font-semibold">{{ t(form.id ? 'coupon.edit' : 'coupon.create') }}</h3><p class="text-sm text-gray-500">{{ t('coupon.templateHint') }}</p>
      <fieldset :disabled="busy" class="grid gap-4 md:grid-cols-3">
        <label>{{ t('coupon.name') }}<input v-model.trim="form.name" class="input" maxlength="100" required /></label>
        <label>{{ t('coupon.kind') }}<select v-model="form.kind" class="input"><option v-for="kind in ['fixed', 'percent', 'bonus']" :key="kind" :value="kind">{{ t('coupon.' + kind) }}</option></select></label>
        <label>{{ t('coupon.currency') }}<select v-model="form.currency" class="input"><option v-for="currency in currencies" :key="currency">{{ currency }}</option></select></label>
        <label>{{ t('coupon.minAmount') }}<input v-model.number="form.min_amount" class="input" type="number" min="0" max="1000000000" step="0.01" required /></label>
        <label>{{ t('coupon.value') }}<input v-model.number="form.value" class="input" type="number" min="0.01" :max="form.kind === 'percent' ? 99.99 : form.kind === 'bonus' ? 1000 : 1000000000" step="0.01" required /></label>
        <label v-if="form.kind !== 'bonus'">{{ t('coupon.maxDiscount') }}<input v-model.number="form.max_discount" class="input" type="number" min="0" max="1000000000" step="0.01" required /></label>
        <label>{{ t('coupon.validDays') }}<input v-model.number="form.valid_days" class="input" type="number" min="1" max="3650" required /></label>
        <label>{{ t('coupon.maxIssues') }}<input v-model.number="form.max_issues" class="input" type="number" min="0" max="10000000" required /></label>
        <label>{{ t('coupon.perUserLimit') }}<input v-model.number="form.per_user_limit" class="input" type="number" min="0" max="10000" required /></label>
        <label class="flex items-center gap-2"><input v-model="form.stackable" type="checkbox" />{{ t('coupon.stackable') }}</label><label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />{{ t('coupon.enabled') }}</label>
      </fieldset><div class="flex gap-3"><button class="btn btn-primary" :disabled="busy">{{ t('common.save') }}</button><button type="button" class="btn btn-secondary" :disabled="busy" @click="form = null">{{ t('common.cancel') }}</button></div>
    </form>
    <section class="card space-y-4 p-6"><h3 class="font-semibold">{{ t('coupon.templates') }}</h3><div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3"><article v-for="c in templates" :key="c.id" class="space-y-2 rounded-xl border p-4 dark:border-dark-700"><div class="flex justify-between gap-3"><strong>{{ c.name }}</strong><span class="text-xs">#{{ c.id }}</span></div><p>{{ rule(c) }}</p><p class="text-sm text-gray-500">{{ t('coupon.issued') }}：{{ c.issued_count }} / {{ c.max_issues || '∞' }} · {{ t(c.enabled ? 'common.enabled' : 'common.disabled') }}</p><div class="flex flex-wrap gap-3"><button class="text-primary-600" @click="form = { ...c }">{{ t('common.edit') }}</button><button class="text-primary-600" :disabled="!c.enabled" @click="issueTemplate = c.id">{{ t('coupon.issue') }}</button><button class="text-primary-600" @click="filterTemplate = c.id; page = 1; loadRecords()">{{ t('coupon.records') }}</button></div></article></div></section>
    <form class="card space-y-4 p-6" @submit.prevent="issue"><h3 class="font-semibold">{{ t('coupon.issue') }}</h3><p class="text-sm text-gray-500">{{ t('coupon.issueHint') }}</p><fieldset class="grid gap-4 md:grid-cols-3" :disabled="busy"><label>{{ t('coupon.template') }}<select v-model.number="issueTemplate" class="input" required><option :value="0" disabled>{{ t('coupon.template') }}</option><option v-for="c in templates.filter(c => c.enabled)" :key="c.id" :value="c.id">#{{ c.id }} {{ c.name }}</option></select></label><label>{{ t('coupon.users') }}<input v-model="userIDs" class="input" required /></label><label>{{ t('coupon.quantity') }}<input v-model.number="quantity" class="input" type="number" min="1" max="100" required /></label></fieldset><button class="btn btn-primary" :disabled="busy || !issueTemplate">{{ t('coupon.issue') }}</button></form>
    <section class="card overflow-hidden"><div class="space-y-3 p-6"><h3 class="font-semibold">{{ t('coupon.records') }} · {{ total }}</h3><form class="flex flex-wrap gap-3" @submit.prevent="page = 1; loadRecords()"><input v-model="search" class="input min-w-48 flex-1" :placeholder="t('coupon.search')" /><select v-model="filterTemplate" class="input w-auto"><option :value="0">{{ t('coupon.templates') }}</option><option v-for="c in templates" :key="c.id" :value="c.id">{{ c.name }}</option></select><select v-model="status" class="input w-auto"><option value="">{{ t('coupon.all') }}</option><option v-for="s in statuses" :key="s" :value="s">{{ t('coupon.' + s) }}</option></select><button class="btn btn-secondary">{{ t('common.search') }}</button></form></div>
      <div class="overflow-x-auto"><table class="w-full min-w-[680px] text-left text-sm"><thead class="bg-gray-50 dark:bg-dark-800"><tr><th class="p-3">ID / {{ t('coupon.name') }}</th><th class="p-3">{{ t('coupon.user') }}</th><th class="p-3">{{ t('coupon.status') }}</th><th class="p-3">{{ t('coupon.source') }}</th><th class="p-3">{{ t('coupon.order') }}</th><th class="p-3">{{ t('coupon.benefit') }}</th><th class="p-3">{{ t('coupon.expires') }}</th><th class="p-3">{{ t('coupon.usedAt') }}</th><th class="p-3"></th></tr></thead><tbody class="divide-y dark:divide-dark-700"><tr v-for="c in items" :key="c.id"><td class="p-3">#{{ c.id }} {{ c.snapshot.name }}</td><td class="p-3">{{ c.user_email || '#' + c.user_id }}</td><td class="p-3">{{ t('coupon.' + c.status) }}</td><td class="p-3">{{ t('coupon.sources.' + c.source, c.source) }}</td><td class="p-3">{{ c.order_id ? '#' + c.order_id : '—' }}<p class="text-xs text-gray-500">{{ c.order_status ? t('payment.status.' + c.order_status.toLowerCase(), c.order_status) : '' }}</p></td><td class="p-3">{{ c.snapshot.currency }} {{ c.discount_amount.toFixed(2) }}<p v-if="c.bonus_amount">+ USD {{ c.bonus_amount.toFixed(2) }}</p></td><td class="whitespace-nowrap p-3">{{ date(c.expires_at) }}</td><td class="whitespace-nowrap p-3">{{ c.used_at ? date(c.used_at) : '—' }}</td><td class="p-3"><button v-if="c.status === 'available'" class="text-red-600" :disabled="busy" @click="revoke(c.id)">{{ t('coupon.revoke') }}</button></td></tr></tbody></table><p v-if="!items.length" class="p-8 text-center text-gray-500">{{ t('coupon.noCoupons') }}</p></div><div class="p-4"><Pagination :page="page" :page-size="20" :total="total" @update:page="page = $event; loadRecords()" /></div>
    </section>
  <CouponUsageHistory admin /></div></AppLayout>
</template>
<script setup lang="ts">
import CouponUsageHistory from '@/components/payment/CouponUsageHistory.vue'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import { adminCouponAPI, type CouponTemplate, type UserCoupon } from '@/api/coupons'
const { t } = useI18n()
const templates = ref<CouponTemplate[]>([]), items = ref<UserCoupon[]>([]), form = ref<CouponTemplate | null>(null)
const issueTemplate = ref(0), quantity = ref(1), userIDs = ref(''), filterTemplate = ref(0), search = ref(''), status = ref(''), page = ref(1), total = ref(0)
const busy = ref(false), error = ref(''), notice = ref('')
const currencies = ['CNY', 'USD', 'EUR', 'HKD', 'JPY', 'GBP', 'SGD', 'AUD', 'CAD'], statuses = ['available', 'reserved', 'used', 'expired', 'revoked']
let lastBatch = '', requestID = '', recordsVersion = 0
function blank(): CouponTemplate { return { id: 0, name: '', kind: 'fixed', currency: 'CNY', min_amount: 100, value: 10, max_discount: 0, valid_days: 7, stackable: false, enabled: true, max_issues: 1000, per_user_limit: 1, issued_count: 0 } }
function date(v: string) { return new Date(v).toLocaleString() }
function rule(c: CouponTemplate) { return t('coupon.' + ({ fixed: 'ruleFixed', percent: 'rulePercent', bonus: 'ruleBonus' }[c.kind]), { currency: c.currency, min: c.min_amount, value: c.value }) }
async function loadTemplates() { templates.value = (await adminCouponAPI.templates()).data }
async function loadRecords() { const v = ++recordsVersion; try { const { data } = await adminCouponAPI.list({ page: page.value, page_size: 20, search: search.value, status: status.value, template_id: filterTemplate.value || undefined }); if (v === recordsVersion) { items.value = data.items; total.value = data.total } } catch (e) { error.value = (e as Error).message } }
async function save() { if (!form.value || busy.value) return; busy.value = true; error.value = ''; try { await adminCouponAPI.save(form.value); form.value = null; notice.value = t('coupon.saved'); await loadTemplates() } catch (e) { error.value = (e as Error).message } finally { busy.value = false } }
async function issue() {
  if (busy.value) return
  const ids = [...new Set(userIDs.value.trim().split(/[\s,，]+/).map(Number))]
  if (!ids.length || ids.length > 100 || ids.some(id => !Number.isSafeInteger(id) || id < 1) || !Number.isInteger(quantity.value) || quantity.value < 1 || quantity.value > 100 || ids.length * quantity.value > 1000) { error.value = t('coupon.invalidUsers'); return }
  const batch = JSON.stringify([issueTemplate.value, ids, quantity.value]); if (batch !== lastBatch) { requestID = crypto.randomUUID(); lastBatch = batch }
  busy.value = true; error.value = ''
  try { const { data } = await adminCouponAPI.issue({ template_id: issueTemplate.value, user_ids: ids, quantity: quantity.value, request_id: requestID }); notice.value = t('coupon.issueSuccess', { count: data.count }); userIDs.value = ''; lastBatch = ''; await loadTemplates(); await loadRecords() } catch (e) { error.value = (e as Error).message } finally { busy.value = false }
}
async function revoke(id: number) { if (!window.confirm(t('coupon.revokeConfirm'))) return; busy.value = true; try { await adminCouponAPI.revoke(id); await loadRecords() } catch (e) { error.value = (e as Error).message } finally { busy.value = false } }
onMounted(async () => { try { await Promise.all([loadTemplates(), loadRecords()]) } catch (e) { error.value = (e as Error).message } })
</script>
