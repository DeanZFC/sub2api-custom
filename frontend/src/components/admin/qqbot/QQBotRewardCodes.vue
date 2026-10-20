<template>
  <div class="space-y-6">
    <section class="card space-y-4 p-5">
      <h2 class="text-lg font-semibold">{{ t('qqbot.redemption.title') }}</h2>
      <p class="text-sm text-gray-500">{{ t('qqbot.redemption.help') }}</p>
      <p class="rounded-lg bg-blue-50 p-3 text-sm text-blue-800 dark:bg-blue-950 dark:text-blue-200">{{ t('qqbot.redemption.security') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-600">{{ notice }}</p>
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="generate">
        <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.batch') }}</span><input v-model="form.batch_name" data-testid="reward-batch" class="input w-full" maxlength="80" required /></label>
        <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.type') }}</span><select v-model="form.reward_type" data-testid="reward-type" class="input w-full"><option v-for="kind in types" :key="kind" :value="kind">{{ t(`qqbot.redemption.types.${kind}`) }}</option></select></label>
        <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.count') }}</span><input v-model.number="form.count" data-testid="reward-count" class="input w-full" type="number" min="1" max="200" step="1" required /></label>
        <label v-if="form.reward_type === 'balance'" class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.amount') }}</span><input v-model.number="form.amount" data-testid="reward-amount" class="input w-full" type="number" min="0.01" max="1000" step="0.01" required /></label>
        <label v-else class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.quantity') }}</span><input v-model.number="form.quantity" data-testid="reward-quantity" class="input w-full" type="number" min="1" max="1000" step="1" required /></label>
        <template v-if="manualReward">
          <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.prize') }}</span><input v-model="form.prize_name" data-testid="reward-prize" class="input w-full" maxlength="100" required /></label>
          <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.instructions') }}</span><textarea v-model="form.claim_instructions" class="input w-full" maxlength="500" :placeholder="t('qqbot.redemption.instructionsPlaceholder')" /></label>
        </template>
        <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.target') }}</span><input v-model="targetUser" data-testid="reward-target" class="input w-full" type="number" min="1" step="1" :placeholder="t('qqbot.redemption.anyUser')" /></label>
        <label class="space-y-1"><span class="text-sm">{{ t('qqbot.redemption.expiry') }}</span><input v-model="expiry" data-testid="reward-expiry" class="input w-full" type="datetime-local" /><span class="block text-xs text-gray-500">{{ t('qqbot.redemption.expiryHelp') }}</span></label>
        <div class="sm:col-span-2"><button class="btn btn-primary" type="submit" :disabled="busy || generated.length > 0" data-testid="reward-generate">{{ t('qqbot.redemption.generate') }}</button></div>
      </form>
      <div v-if="generated.length" class="space-y-3 rounded-lg border border-amber-300 p-4" data-testid="generated-codes">
        <p class="text-sm font-medium">{{ t('qqbot.redemption.once') }}</p>
        <textarea :value="generatedText" class="input h-40 w-full font-mono text-sm" readonly :aria-label="t('qqbot.redemption.generated')" />
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-secondary" type="button" @click="copyCodes">{{ t('qqbot.redemption.copy') }}</button>
          <button class="btn btn-secondary" type="button" @click="downloadCodes">{{ t('qqbot.redemption.download') }}</button>
          <button class="btn btn-secondary" type="button" @click="generated = []">{{ t('qqbot.redemption.clear') }}</button>
        </div>
      </div>
    </section>

    <section class="card space-y-4 p-5">
      <h2 class="text-lg font-semibold">{{ t('qqbot.redemption.records') }}</h2>
      <form class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_180px_auto]" @submit.prevent="page = 1; load()">
        <input v-model="search" class="input min-w-0 flex-1" :placeholder="t('qqbot.redemption.search')" :aria-label="t('qqbot.redemption.search')" maxlength="254" />
        <select v-model="status" class="input" :aria-label="t('qqbot.redemption.status')"><option value="">{{ t('qqbot.redemption.all') }}</option><option v-for="value in statuses" :key="value" :value="value">{{ t(`qqbot.redemption.statuses.${value}`) }}</option></select>
        <button class="btn btn-secondary" type="submit" :disabled="loading">{{ t('qqbot.redemption.searchButton') }}</button>
      </form>
      <p v-if="listError" role="alert" class="text-sm text-red-600">{{ listError }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left text-sm">
          <thead><tr class="border-b dark:border-dark-700"><th class="p-2">{{ t('qqbot.redemption.code') }}</th><th class="p-2">{{ t('qqbot.redemption.reward') }}</th><th class="p-2">{{ t('qqbot.redemption.status') }}</th><th class="p-2">{{ t('qqbot.redemption.recipient') }}</th><th class="p-2">{{ t('qqbot.redemption.actions') }}</th></tr></thead>
          <tbody><tr v-for="row in items" :key="row.id" class="border-b align-top dark:border-dark-700">
            <td class="space-y-1 p-2"><div>#{{ row.id }} · …{{ row.code_hint }}</div><div>{{ row.batch_name }}</div><div class="text-xs text-gray-500">AppID: {{ row.app_id }}</div><div class="text-xs text-gray-500">{{ t('qqbot.redemption.expiry') }}: {{ date(row.expires_at) }}</div></td>
            <td class="space-y-1 p-2"><div>{{ t(`qqbot.redemption.types.${row.reward_type}`) }}</div><div>{{ row.reward_type === 'balance' ? `USD ${row.amount.toFixed(2)}` : `${row.prize_name || t('qqbot.redemption.types.chances')} × ${row.quantity}` }}</div><div v-if="row.claim_instructions" class="max-w-xs whitespace-pre-wrap text-xs text-gray-500">{{ row.claim_instructions }}</div></td>
            <td class="space-y-1 p-2"><div>{{ t(`qqbot.redemption.statuses.${row.status}`) }}</div><div v-if="row.redeemed_at" class="text-xs text-gray-500">{{ t('qqbot.redemption.redeemed') }}: {{ date(row.redeemed_at) }}</div><div v-if="row.fulfilled_at" class="text-xs text-gray-500">{{ t('qqbot.redemption.fulfilled') }}: {{ date(row.fulfilled_at) }}</div><div v-if="row.fulfillment_note" class="max-w-xs whitespace-pre-wrap text-xs text-gray-500">{{ row.fulfillment_note }}</div></td>
            <td class="space-y-1 p-2"><div>{{ row.email || (row.user_id ? `#${row.user_id}` : '—') }}</div><div v-if="row.member_id" class="max-w-[12rem] break-all text-xs text-gray-500">{{ row.member_id }}</div><div v-if="row.target_user_id" class="text-xs text-gray-500">{{ t('qqbot.redemption.target') }}: #{{ row.target_user_id }}</div></td>
            <td class="p-2"><button v-if="row.status === 'unused' || row.status === 'expired' || row.status === 'pending'" class="btn btn-secondary whitespace-nowrap" :disabled="busy" type="button" @click="selected = row; note = ''">{{ t(row.status === 'pending' ? 'qqbot.redemption.fulfill' : 'qqbot.redemption.disable') }}</button></td>
          </tr><tr v-if="!items.length"><td colspan="5" class="p-6 text-center text-gray-500">{{ t('qqbot.redemption.empty') }}</td></tr></tbody>
        </table>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-2"><span class="text-sm text-gray-500">{{ t('qqbot.recordsPage', { total, page, pages: Math.max(1, Math.ceil(total / 20)) }) }}</span><div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading || page <= 1" @click="page--; load()">{{ t('qqbot.redemption.previousPage') }}</button><button class="btn btn-secondary" :disabled="loading || page * 20 >= total" @click="page++; load()">{{ t('qqbot.redemption.nextPage') }}</button></div></div>
      <form v-if="selected" class="space-y-3 rounded-lg border border-amber-300 p-4" @submit.prevent="update">
        <p>{{ t(selected.status === 'pending' ? 'qqbot.redemption.fulfillConfirm' : 'qqbot.redemption.disableConfirm', { id: selected.id }) }}</p>
        <label v-if="selected.status === 'pending'" class="block space-y-1"><span class="text-sm">{{ t('qqbot.redemption.note') }}</span><textarea v-model="note" class="input w-full" maxlength="500" /></label>
        <div class="flex gap-2"><button class="btn btn-primary" :disabled="busy" type="submit">{{ t('common.confirm') }}</button><button class="btn btn-secondary" :disabled="busy" type="button" @click="selected = null">{{ t('common.cancel') }}</button></div>
      </form>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { qqbotAPI, type QQGeneratedRewardCode, type QQRewardCode, type QQRewardCodeCreate, type QQRewardType } from '@/api/qqbot'

const { t } = useI18n()
const types: QQRewardType[] = ['balance', 'chances', 'physical', 'other']
const statuses = ['unused', 'pending', 'fulfilled', 'disabled', 'expired'] as const
const form = ref<QQRewardCodeCreate>({ batch_name: '', reward_type: 'balance', count: 1, amount: 10, quantity: 1, prize_name: '', claim_instructions: '', target_user_id: null, expires_at: null })
const manualReward = computed(() => ['physical', 'other'].includes(form.value.reward_type))
const targetUser = ref(''), expiry = ref(''), error = ref(''), listError = ref(''), notice = ref(''), note = ref('')
const busy = ref(false), loading = ref(false), generated = ref<QQGeneratedRewardCode[]>([])
const generatedText = computed(() => generated.value.map(item => item.code).join('\n'))
const items = ref<QQRewardCode[]>([]), selected = ref<QQRewardCode | null>(null)
const total = ref(0), page = ref(1), search = ref(''), status = ref('')
let loadVersion = 0
const date = (value: string | null) => value ? new Date(value).toLocaleString() : '—'
const message = (e: unknown) => (e as Error)?.message || t('common.error')

async function load() {
  const version = ++loadVersion
  loading.value = true; listError.value = ''
  try {
    const data = await qqbotAPI.rewardCodes(search.value.trim(), status.value, page.value)
    if (version !== loadVersion) return
    items.value = data.items; total.value = data.total
  } catch (e) { if (version === loadVersion) { items.value = []; listError.value = message(e) } }
  finally { if (version === loadVersion) loading.value = false }
}
async function generate() {
  if (busy.value || generated.value.length) return
  error.value = ''; notice.value = ''
  const f = form.value, uid = targetUser.value === '' ? null : Number(targetUser.value)
  const expires = expiry.value ? new Date(expiry.value) : null
  if (!f.batch_name.trim() || !Number.isInteger(f.count) || f.count < 1 || f.count > 200 ||
      (uid !== null && (!Number.isSafeInteger(uid) || uid < 1)) ||
      (expires && (!Number.isFinite(expires.getTime()) || expires.getTime() <= Date.now())) ||
      (f.reward_type === 'balance' ? (!Number.isFinite(f.amount) || f.amount < 0.01 || f.amount > 1000 || Math.abs(f.amount * 100 - Math.round(f.amount * 100)) > 0.000001) : (!Number.isInteger(f.quantity) || f.quantity < 1 || f.quantity > 1000)) ||
      (manualReward.value && !f.prize_name.trim())) { error.value = t('qqbot.redemption.invalid'); return }
  busy.value = true
  try {
    generated.value = await qqbotAPI.createRewardCodes({ ...f, batch_name: f.batch_name.trim(), amount: f.reward_type === 'balance' ? f.amount : 0, quantity: f.reward_type === 'balance' ? 0 : f.quantity, prize_name: manualReward.value ? f.prize_name.trim() : '', claim_instructions: manualReward.value ? f.claim_instructions.trim() : '', target_user_id: uid, expires_at: expires?.toISOString() ?? null })
    page.value = 1; search.value = ''; status.value = ''; await load()
  } catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function update() {
  if (!selected.value || busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await qqbotAPI.updateRewardCode(selected.value.id, selected.value.status === 'pending' ? 'fulfill' : 'disable', note.value); selected.value = null; notice.value = t('common.saved'); await load() }
  catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function copyCodes() {
  try { await navigator.clipboard.writeText(generatedText.value); notice.value = t('qqbot.redemption.copied') }
  catch { error.value = t('qqbot.redemption.copyFailed') }
}
function downloadCodes() {
  const url = URL.createObjectURL(new Blob([generatedText.value + '\n'], { type: 'text/plain;charset=utf-8' }))
  const link = document.createElement('a'); link.href = url; link.download = 'qq-reward-codes.txt'; link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
onMounted(load)
onBeforeUnmount(() => { generated.value = []; loadVersion++ })
</script>
